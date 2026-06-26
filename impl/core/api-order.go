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

	// Tax rate from the order's existing totals (decimal, e.g. 0.23 for 23% VAT).
	taxRate := orderParams.TaxRate() / 100

	// Dedupe by ZohoID first: Zoho subforms can carry the same product on multiple lines
	// (split discounts, free gifts). Without merging, each line would become a separate
	// order_product row pointing at the same product_id.
	mergedItems := mergeItemsByZohoID(orderDetails.OrderedItems)

	// Reconstruct OpenCart's order_total rows and per-line product data from the payload.
	totals := c.computeReverseTotals(mergedItems, orderDetails.GrandTotal, orderDetails.Coupon, taxRate)

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

// computeReverseTotals rebuilds OpenCart's order_total rows and order_product data from a
// Zoho reverse-sync payload. items must already be deduped by ZohoID; taxRate is the
// order's existing VAT rate as a decimal (e.g. 0.23 for 23%).
//
// OpenCart carries two reductions that sit on opposite sides of VAT, and they are
// reconstructed differently:
//   - coupon   (couponCode != ""): PRE-tax. Lines arrive at list price; the coupon is a
//     percentage of the subtotal, VAT is charged on the reduced base, and the coupon is
//     stored as a negative order_total.coupon. sub_total + tax + coupon == total.
//   - discount (couponCode == ""): POST-tax (e.g. first-buy 10% of gross). VAT is charged
//     on the full line subtotal, then the discount comes off the gross total. It is the gap
//     between (items + tax) and grand_total, stored as a negative order_total.discount. No
//     meaningful gap means no discount, and any sub-cent remainder folds into tax so the
//     rows stay exact.
func (c *Core) computeReverseTotals(items []entity.ApiOrderedItem, grandTotalFloat float64, couponCode string, taxRate float64) reverseTotals {
	hasCoupon := couponCode != ""

	var discountPercent float64
	if hasCoupon {
		discountPercent = c.calculateDiscountPercent(items)
	}

	var itemsTotal, shippingTotal int64
	products := make([]sql.OrderProductData, 0, len(items))
	for _, item := range items {
		if item.ZohoID == c.shippingItemZohoId {
			shippingTotal += int64(math.Round(item.Price * 100))
			continue
		}

		// For coupon orders keep ListPrice (the reduction lives at order level). Otherwise
		// derive from Total/Quantity so any per-line special price is reflected.
		var paidUnit float64
		if hasCoupon || item.Quantity == 0 {
			paidUnit = item.Price
		} else {
			paidUnit = item.Total / float64(item.Quantity)
		}

		taxPerUnit := paidUnit * taxRate
		lineTotal := paidUnit * float64(item.Quantity)

		products = append(products, sql.OrderProductData{
			ZohoID:       item.ZohoID,
			Quantity:     item.Quantity,
			PriceInCents: int64(math.Round(paidUnit * 100)),
			TotalInCents: int64(math.Round(lineTotal * 100)),
			TaxInCents:   int64(math.Round(taxPerUnit * 100)),
		})

		itemsTotal += int64(math.Round(lineTotal * 100))
	}

	grandTotal := int64(math.Round(grandTotalFloat * 100))

	var taxTotal, discount, coupon int64
	if hasCoupon {
		couponAmt := int64(math.Round(float64(itemsTotal) * discountPercent))
		netBase := itemsTotal - couponAmt
		taxTotal = grandTotal - netBase - shippingTotal
		coupon = -couponAmt
	} else {
		expectedTax := int64(math.Round(float64(itemsTotal) * taxRate))
		postTaxDiscount := itemsTotal + expectedTax + shippingTotal - grandTotal
		if postTaxDiscount > 1 {
			taxTotal = expectedTax
			discount = -postTaxDiscount
		} else {
			taxTotal = grandTotal - itemsTotal - shippingTotal
			discount = 0
		}
	}

	return reverseTotals{
		Products:   products,
		ItemsTotal: itemsTotal,
		Shipping:   shippingTotal,
		Tax:        taxTotal,
		Discount:   discount,
		Coupon:     coupon,
		Total:      grandTotal,
	}
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
