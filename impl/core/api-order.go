package core

import (
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/database/sql"
	"zohoclient/internal/lib/sl"
)

func (c *Core) UpdateOrder(orderDetails *entity.ApiOrder) error {
	// zoho_id is the only correlation available until we resolve the OpenCart
	// order_id — attach it to the base log so pre-resolution messages aren't orphan.
	log := c.log.With(
		sl.Module("core.UpdateOrder"),
		slog.String("zoho_id", orderDetails.ZohoID),
	)

	if orderDetails.ZohoID == "" {
		return fmt.Errorf("zoho_id is required")
	}

	// Retry logic to handle race condition: webhook may arrive before zoho_id is saved to database
	const maxRetries = 5
	const retryDelay = 200 * time.Millisecond

	var orderId int64
	var orderParams *entity.CheckoutParams
	var err error

	for attempt := 0; attempt < maxRetries; attempt++ {
		orderId, orderParams, err = c.repo.OrderSearchByZohoId(orderDetails.ZohoID)
		if err == nil {
			break
		}
		if attempt < maxRetries-1 {
			log.With(slog.Int("attempt", attempt+1)).Debug("order not found, retrying...")
			time.Sleep(retryDelay)
		}
	}
	if err != nil {
		log.With(slog.Int("attempts", maxRetries), sl.Err(err)).
			Warn("order not found, dropping update")
		return fmt.Errorf("order not found after %d attempts: %w", maxRetries, err)
	}

	currencyValue := orderParams.CurrencyValue

	// order_id known from here on — add it to the base log so every downstream
	// message (errors, diff, suppression) is correlated.
	log = log.With(slog.Int64("order_id", orderId))

	// Echo suppression: compare the payload's Modified_Time against the value we
	// stored after our own last write to Zoho. If the payload is older or equal,
	// this is our own write coming back as a webhook — skip it. Missing/unparseable
	// payload timestamps fall through (we have no way to dedupe and must apply).
	incomingModified, hasIncoming := parseZohoTime(orderDetails.ModifiedTime)
	if hasIncoming {
		storedModified, err := c.repo.GetOrderZohoModifiedTime(orderId)
		if err != nil {
			log.With(sl.Err(err)).Error("failed to get zoho_modified_time")
			return fmt.Errorf("failed to get zoho_modified_time: %w", err)
		}
		if !storedModified.IsZero() && !incomingModified.After(storedModified) {
			log.With(
				slog.Time("incoming", incomingModified),
				slog.Time("stored", storedModified),
			).Debug("skipping echo webhook")
			return nil
		}
	} else {
		// Investigating: see how often inbound payloads arrive without a usable
		// Modified_Time. Log the raw value so we can tell "field absent" from
		// "field present but unparseable", and the stored value for context.
		storedModified, getErr := c.repo.GetOrderZohoModifiedTime(orderId)
		log.With(
			slog.String("raw_modified_time", orderDetails.ModifiedTime),
			slog.Time("stored", storedModified),
			slog.Bool("stored_present", !storedModified.IsZero()),
			slog.Any("stored_err", getErr),
		).Debug("inbound payload has no usable Modified_Time, echo suppression bypassed")
	}

	// Snapshot current items + status + total so we can report what the webhook
	// actually changed once the transaction commits.
	previousItems, err := c.repo.GetOrderProductsSummary(orderId)
	if err != nil {
		log.With(sl.Err(err)).Error("failed to load current items")
		return fmt.Errorf("failed to load current items: %w", err)
	}
	previousStatusId := orderParams.StatusId
	previousTotal := orderParams.Total

	// Resolve the new status, but defer the write to the transaction so a TX failure
	// can't leave the order with a new status and stale items.
	newStatusId := previousStatusId
	if orderDetails.Status != "" {
		statusId := c.GetStatusIdByName(orderDetails.Status)
		if statusId > 0 {
			log = log.With(slog.Int("status_id", statusId))
			newStatusId = statusId
		} else {
			log.With(slog.String("status", orderDetails.Status)).
				Warn("unknown status name from Zoho, keeping current")
		}
	}

	// The order's VAT rate (decimal, e.g. 0.23 for 23% VAT).
	taxRate := orderParams.VatRate()

	// Dedupe by ZohoID first: Zoho subforms can carry the same product on multiple lines
	// (split discounts, free gifts). Without merging, each line would become a separate
	// order_product row pointing at the same product_id.
	mergedItems := mergeItemsByZohoID(orderDetails.OrderedItems)

	// Zoho pings us on every status change, and the subform is usually untouched. Rewriting the
	// items and totals on such a webhook would restate figures nobody changed — and on an order
	// that predates the OpenCart VAT fix (docs/OPENCART_VAT_BUG_RU.md) it would silently rewrite
	// its tax and coupon rows into their lawful equivalents, altering numbers that may already
	// have been declared. So when the subform still matches what OpenCart holds, move the status
	// and touch nothing else.
	if itemsUnchanged(mergedItems, orderParams, c.shippingItemZohoId) {
		if newStatusId != previousStatusId {
			if err := c.repo.ChangeOrderStatus(orderId, int64(newStatusId), "Updated via API"); err != nil {
				log.With(sl.Err(err)).Error("failed to update order status")
				return fmt.Errorf("failed to update order status: %w", err)
			}
		}
		if hasIncoming {
			if err := c.repo.SetOrderZohoModifiedTime(orderId, incomingModified); err != nil {
				log.With(sl.Err(err)).Warn("store zoho_modified_time failed")
			}
		}
		c.saveOrderVersionToMongo(orderId, orderDetails)
		log.With(
			slog.Int("status_from", previousStatusId),
			slog.Int("status_to", newStatusId),
		).Info("order update applied (status only, items and totals untouched)")
		return nil
	}

	// Reconstruct OpenCart's order_total rows and per-line product data from the payload.
	totals := c.computeReverseTotals(mergedItems, orderDetails.GrandTotal, orderParams)

	// Execute entire update in a single transaction
	txData := sql.OrderUpdateTransaction{
		OrderID:       orderId,
		Items:         totals.Products,
		CurrencyValue: currencyValue,
		OrderTotal:    totals.Total,
		Totals: sql.OrderTotalsData{
			SubTotal: totals.ItemsTotal,
			Tax:      totals.Tax,
			Discount: totals.Discount,
			Shipping: totals.Shipping,
			Total:    totals.Total,
			Coupon:   totals.Coupon,
		},
		NewStatusID:   int64(newStatusId),
		StatusComment: "Updated via API",
	}

	err = c.repo.UpdateOrderWithTransaction(txData)
	if err != nil {
		log.With(sl.Err(err)).Error("failed to update order")
		return fmt.Errorf("failed to update order: %w", err)
	}

	// Record the version we just applied so a future echo for this same change is
	// recognised and suppressed. Failure is non-fatal — worst case the next echo
	// triggers an idempotent reload.
	if hasIncoming {
		if err := c.repo.SetOrderZohoModifiedTime(orderId, incomingModified); err != nil {
			log.With(sl.Err(err)).Warn("store zoho_modified_time failed")
		}
	}

	// Audit what the webhook actually changed.
	newTotalDisplay := (float64(totals.Total) / 100) / currencyValue
	logOrderDiff(log, previousItems, mergedItems, c.shippingItemZohoId,
		previousStatusId, newStatusId,
		previousTotal, newTotalDisplay, orderParams.Currency)

	// Save order version to MongoDB
	c.saveOrderVersionToMongo(orderId, orderDetails)

	log.With(
		slog.String("sub_total", fmtCents(totals.ItemsTotal)),
		slog.String("shipping", fmtCents(totals.Shipping)),
		slog.String("discount", fmtCents(totals.Discount)),
		slog.String("coupon", fmtCents(totals.Coupon)),
		slog.String("tax_total", fmtCents(totals.Tax)),
		slog.Float64("tax_rate", round4(taxRate)),
		slog.String("total", fmtCents(totals.Total)),
		slog.String("zoho_total", fmtCents(int64(math.Round(orderDetails.GrandTotal*100)))),
	).Debug("order updated")

	return nil
}

// reverseTotals is the OpenCart order_total breakdown (in cents) plus the per-line
// order_product rows reconstructed from a Zoho reverse-sync payload. Discount and Coupon
// follow OpenCart's negative-sign convention.
type reverseTotals struct {
	Products   []sql.OrderProductData
	ItemsTotal int64 // sub_total
	Shipping   int64
	Tax        int64
	Discount   int64 // post-tax discount, negative
	Coupon     int64 // pre-tax coupon, negative
	Total      int64
}

// computeReverseTotals rebuilds OpenCart's order_total rows and order_product data from a Zoho
// reverse-sync payload. items must already be deduped by ZohoID; oc is the order as OpenCart
// currently holds it, used to recover information the payload cannot carry.
//
// Zoho derives its grand total as Sub_Total x (1 + VAT%), where Sub_Total is the sum of the
// subform line totals (which Zoho recomputes itself from ListPrice and DiscountP — the Total we
// send is ignored). Since every at-sale reduction lowers the VAT base, the whole reduction is
// carried in the lines and nothing is hidden at order level:
//
//	itemsTotal = sum(qty x price actually paid)      -> order_total.sub_total
//	netLines   = sum(line totals Zoho recomputed)
//	tax        = (netLines + shipping) x rate        -> VAT sits on the discounted lines
//	reduction  = itemsTotal + shipping + tax - grandTotal
//
// reduction is derived from the other rows rather than measured, so the rows always sum to the
// grand total and OpenCart stays internally consistent even after a manager edits the subform.
// The payload cannot say how that reduction divides between the coupon and discount rows, so
// the split comes from the order's stored totals.
func (c *Core) computeReverseTotals(items []entity.ApiOrderedItem, grandTotalFloat float64, oc *entity.CheckoutParams) reverseTotals {
	rate := oc.VatRate()

	// Zoho's ListPrice may be the catalogue (master) price, while OpenCart's sub_total is built
	// from the price actually paid. Recover the paid price per product from the stored order.
	paidPrices := make(map[string]float64, len(oc.LineItems))
	for _, li := range oc.LineItems {
		if li != nil && li.ZohoId != "" && li.Price > 0 {
			paidPrices[li.ZohoId] = li.Price
		}
	}

	var itemsTotalF, netProductsF, shippingF float64
	products := make([]sql.OrderProductData, 0, len(items))
	for _, item := range items {
		if item.ZohoID == c.shippingItemZohoId {
			shippingF += item.Price
			continue
		}

		// A product added by the manager in Zoho has no OpenCart counterpart: fall back to the
		// list price it arrived with.
		paidUnit, ok := paidPrices[item.ZohoID]
		if !ok {
			paidUnit = item.Price
		}

		lineTotal := paidUnit * float64(item.Quantity)
		itemsTotalF += lineTotal
		netProductsF += item.Total

		products = append(products, sql.OrderProductData{
			ZohoID:       item.ZohoID,
			Quantity:     item.Quantity,
			PriceInCents: cents(paidUnit),
			TotalInCents: cents(lineTotal),
			TaxInCents:   cents(paidUnit * rate),
		})
	}

	grandTotal := cents(grandTotalFloat)
	itemsTotal := cents(itemsTotalF)
	shipping := cents(shippingF)
	taxTotal := cents((netProductsF + shippingF) * rate)

	// Everything the lines gave up. Derived so the rows always sum to the grand total; a
	// non-positive result means there was no reduction, and any remainder folds into tax so the
	// rows stay exact.
	reduction := itemsTotal + shipping + taxTotal - grandTotal
	if reduction <= 1 {
		reduction = 0
		taxTotal = grandTotal - itemsTotal - shipping
	}

	coupon, discount := c.splitReductions(oc, reduction)

	return reverseTotals{
		Products:   products,
		ItemsTotal: itemsTotal,
		Shipping:   shipping,
		Tax:        taxTotal,
		Discount:   discount,
		Coupon:     coupon,
		Total:      grandTotal,
	}
}

// itemsUnchanged reports whether the Zoho subform still matches the products OpenCart holds:
// the same zoho_ids in the same quantities. The shipping pseudo-product is ignored on both
// sides — it is not an OpenCart order_product, it rides in the shipping order_total row.
func itemsUnchanged(items []entity.ApiOrderedItem, oc *entity.CheckoutParams, shippingZohoID string) bool {
	stored := make(map[string]int, len(oc.LineItems))
	for _, li := range oc.LineItems {
		if li == nil || li.ZohoId == "" || li.ZohoId == shippingZohoID {
			continue
		}
		stored[li.ZohoId] += int(li.Qty)
	}

	seen := 0
	for _, it := range items {
		if it.ZohoID == shippingZohoID {
			continue
		}
		if stored[it.ZohoID] != it.Quantity {
			return false
		}
		seen++
	}
	return seen == len(stored)
}

// splitReductions divides the reduction recovered from a Zoho payload between OpenCart's coupon
// and discount rows (both stored negative). The lines carry the two blended into one figure and
// the payload cannot separate them, so when an order has both they are split in the same
// proportion the stored order used.
func (c *Core) splitReductions(oc *entity.CheckoutParams, reduction int64) (coupon, discount int64) {
	couponAmt := math.Abs(oc.Coupon)
	discountAmt := math.Abs(oc.Discount)

	switch {
	case reduction <= 0 || couponAmt+discountAmt == 0:
		return 0, 0
	case discountAmt == 0:
		return -reduction, 0
	case couponAmt == 0:
		return 0, -reduction
	default:
		share := couponAmt / (couponAmt + discountAmt)
		couponPart := int64(math.Round(float64(reduction) * share))
		return -couponPart, -(reduction - couponPart)
	}
}

// cents converts a currency amount to integer cents.
func cents(v float64) int64 {
	return int64(math.Round(v * 100))
}

// calculateTaxRate calculates the tax rate from existing order_total data.
// Returns tax rate as a decimal (e.g., 0.23 for 23% VAT), rounded to 4 decimal places.
func (c *Core) calculateTaxRate(orderId int64) (float64, error) {
	// Get sub_total and tax from order_total table
	_, subTotal, err := c.repo.OrderTotal(orderId, "sub_total")
	if err != nil {
		return 0, fmt.Errorf("failed to get sub_total: %w", err)
	}

	_, tax, err := c.repo.OrderTotal(orderId, "tax")
	if err != nil {
		return 0, fmt.Errorf("failed to get tax: %w", err)
	}

	if subTotal == 0 {
		return 0, fmt.Errorf("sub_total is zero")
	}

	// Calculate rate and round to 4 decimals
	rate := tax / subTotal
	return math.Round(rate*10000) / 10000, nil
}

// parseZohoTime parses a Zoho CRM datetime string. Zoho returns RFC3339 timestamps
// (e.g. "2024-01-15T10:30:00+02:00"); a few legacy callers also emit the variant
// without seconds. Returns the parsed time in UTC and a bool indicating success.
func parseZohoTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// mergeItemsByZohoID collapses items sharing a ZohoID into a single line.
// Quantity and Total are summed; Price is recomputed from the combined Total/Quantity
// so downstream paid-unit math stays consistent (falls back to the first seen Price
// when Quantity is zero).
func mergeItemsByZohoID(items []entity.ApiOrderedItem) []entity.ApiOrderedItem {
	merged := make([]entity.ApiOrderedItem, 0, len(items))
	index := make(map[string]int, len(items))

	for _, item := range items {
		if pos, ok := index[item.ZohoID]; ok {
			merged[pos].Quantity += item.Quantity
			merged[pos].Total += item.Total
			if merged[pos].Quantity > 0 {
				merged[pos].Price = merged[pos].Total / float64(merged[pos].Quantity)
			}
			continue
		}
		index[item.ZohoID] = len(merged)
		merged = append(merged, item)
	}

	return merged
}

// calculateDiscountPercent calculates the discount percentage from API items.
// Compares API totals (discounted) vs full totals (price × quantity).
// Returns discount as a decimal (e.g., 0.15 for 15% discount).
func (c *Core) calculateDiscountPercent(items []entity.ApiOrderedItem) float64 {
	var sumApiTotals float64 = 0
	var sumFullTotals float64 = 0

	for _, item := range items {
		if c.shippingItemZohoId != "" && item.ZohoID == c.shippingItemZohoId {
			continue
		}
		sumApiTotals += item.Total                           // Discounted total from API
		sumFullTotals += item.Price * float64(item.Quantity) // Full price
	}

	if sumFullTotals == 0 {
		return 0
	}

	return 1.0 - (sumApiTotals / sumFullTotals)
}

// itemDiffEntry is one aggregated line used for before/after comparison.
type itemDiffEntry struct {
	Name     string
	Quantity int
}

// logOrderDiff emits a single Info log describing what the webhook changed:
// status (if any), grand total (if changed), and items added/removed/changed.
// The shipping pseudo-product is excluded from item diffs.
func logOrderDiff(
	log *slog.Logger,
	previous []sql.OrderProductSummary,
	incoming []entity.ApiOrderedItem,
	shippingZohoID string,
	previousStatusId, newStatusId int,
	previousTotal, newTotal float64,
	currency string,
) {
	prevMap := make(map[string]itemDiffEntry, len(previous))
	for _, it := range previous {
		if shippingZohoID != "" && it.ZohoID == shippingZohoID {
			continue
		}
		key := it.ZohoID
		if key == "" {
			// Unmapped products are still worth surfacing; fall back to the name
			// so they aren't all collapsed into one bucket.
			key = "name:" + it.Name
		}
		e := prevMap[key]
		e.Name = it.Name
		e.Quantity += it.Quantity
		prevMap[key] = e
	}

	newMap := make(map[string]itemDiffEntry, len(incoming))
	for _, it := range incoming {
		if shippingZohoID != "" && it.ZohoID == shippingZohoID {
			continue
		}
		e := newMap[it.ZohoID]
		e.Quantity += it.Quantity
		newMap[it.ZohoID] = e
	}

	var added, removed, changed []string
	for zid, n := range newMap {
		o, ok := prevMap[zid]
		if !ok {
			added = append(added, fmt.Sprintf("%s x%d", zid, n.Quantity))
			continue
		}
		if o.Quantity != n.Quantity {
			label := zid
			if o.Name != "" {
				label = fmt.Sprintf("%s (%s)", o.Name, zid)
			}
			changed = append(changed, fmt.Sprintf("%s %d→%d", label, o.Quantity, n.Quantity))
		}
	}
	for zid, o := range prevMap {
		if _, ok := newMap[zid]; ok {
			continue
		}
		label := zid
		if o.Name != "" {
			label = fmt.Sprintf("%s (%s)", o.Name, zid)
		}
		removed = append(removed, fmt.Sprintf("%s x%d", label, o.Quantity))
	}

	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)

	totalDelta := newTotal - previousTotal
	totalChanged := math.Abs(totalDelta) >= 0.005 // ignore sub-cent float jitter
	statusChanged := previousStatusId != newStatusId

	if !totalChanged && !statusChanged && len(added) == 0 && len(removed) == 0 && len(changed) == 0 {
		log.Info("order update applied (no observable changes)")
		return
	}

	attrs := []any{}
	if statusChanged {
		attrs = append(attrs,
			slog.Int("status_from", previousStatusId),
			slog.Int("status_to", newStatusId),
		)
	}
	if totalChanged {
		attrs = append(attrs,
			slog.String("total_from", fmt.Sprintf("%.2f %s", previousTotal, currency)),
			slog.String("total_to", fmt.Sprintf("%.2f %s", newTotal, currency)),
			slog.String("total_delta", fmt.Sprintf("%+.2f %s", totalDelta, currency)),
		)
	}
	if len(added) > 0 {
		attrs = append(attrs, slog.String("products_added", strings.Join(added, ", ")))
	}
	if len(removed) > 0 {
		attrs = append(attrs, slog.String("products_removed", strings.Join(removed, ", ")))
	}
	if len(changed) > 0 {
		attrs = append(attrs, slog.String("products_changed", strings.Join(changed, ", ")))
	}

	log.With(attrs...).Info("order update applied")
}
