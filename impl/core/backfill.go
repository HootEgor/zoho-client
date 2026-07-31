package core

import (
	"fmt"
	"log/slog"
	"math"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
)

// discountEpsilon is the smallest difference worth an API call. Zoho stores DiscountP to 2
// decimals and List_Price to 4, so anything below half a unit of the last digit is noise.
const (
	discountEpsilon  = 0.005
	listPriceEpsilon = 0.00005
)

// BackfillResult is what a backfill run did, for the closing report.
type BackfillResult struct {
	Scanned   int
	Corrected int // orders whose rows differ and were (or would be) rewritten
	Unchanged int // already correct
	Skipped   int // subform no longer matches what OpenCart holds
	Failed    int
}

// BackfillOrderDiscounts repairs orders synced with the shipping-VAT bug, which inflated the
// per-line discount by the VAT on carriage: the lines were discounted to pay for a VAT Zoho never
// charges on the non-taxable shipping item, leaving Zoho's grand total short by Shipping x rate.
//
// For each order placed in [from, to) that carries a Zoho id, the corrected subform is rebuilt
// with the current buildZohoOrder and compared row by row against what Zoho holds. Only
// List_Price and DiscountP are rewritten, and only on rows that still line up with OpenCart's
// items — an order a manager has since edited is reported and left alone.
//
// The rows are updated in place by their Zoho subform row id. Sending them without ids would
// append duplicates rather than correct anything, so a row that arrives without one aborts the
// order (see ZohoService.UpdateOrderItemRows).
//
// With apply=false nothing is written: the run reports what it would change.
func (c *Core) BackfillOrderDiscounts(from, to time.Time, apply bool) (BackfillResult, error) {
	var res BackfillResult

	if c.repo == nil || c.zoho == nil {
		return res, fmt.Errorf("backfill needs both the database and the Zoho service")
	}

	log := c.log.With(
		sl.Module("backfill"),
		slog.String("from", from.Format(time.DateOnly)),
		slog.String("to", to.Format(time.DateOnly)),
		slog.Bool("apply", apply),
	)

	orders, err := c.repo.OrdersSyncedBetween(from, to)
	if err != nil {
		return res, fmt.Errorf("load synced orders: %w", err)
	}

	log.With(slog.Int("orders", len(orders))).Info("backfill started")

	for _, synced := range orders {
		res.Scanned++
		oc := synced.Order
		olog := log.With(
			slog.Int64("order_id", oc.OrderId),
			slog.String("zoho_id", synced.ZohoID),
			slog.Float64("shipping", round2(oc.Shipping)),
		)

		patches, err := c.backfillPatches(synced.ZohoID, oc)
		if err != nil {
			res.Skipped++
			olog.With(sl.Err(err)).Warn("order skipped")
			continue
		}
		if len(patches) == 0 {
			res.Unchanged++
			olog.Debug("order already correct")
			continue
		}

		res.Corrected++
		olog = olog.With(slog.Int("rows", len(patches)))

		if !apply {
			olog.Info("would correct order (dry run)")
			continue
		}

		modified, err := c.zoho.UpdateOrderItemRows(synced.ZohoID, patches)
		if err != nil {
			res.Corrected--
			res.Failed++
			olog.With(sl.Err(err)).Error("order not corrected")
			continue
		}

		// Our own write comes back as a webhook; record the version so it is recognised as an
		// echo rather than reverse-synced into OpenCart.
		if t, err := time.Parse(time.RFC3339, modified); err == nil {
			if err = c.repo.SetOrderZohoModifiedTime(oc.OrderId, t); err != nil {
				olog.With(sl.Err(err)).Warn("store zoho_modified_time failed")
			}
		}

		olog.Info("order corrected")
	}

	log.With(
		slog.Int("scanned", res.Scanned),
		slog.Int("corrected", res.Corrected),
		slog.Int("unchanged", res.Unchanged),
		slog.Int("skipped", res.Skipped),
		slog.Int("failed", res.Failed),
	).Info("backfill finished")

	return res, nil
}

// backfillPatches returns the subform rows that need rewriting for one order, or an empty slice
// when Zoho already holds the right figures. It errors when the Zoho subform no longer lines up
// with the OpenCart order, which means the record was edited and must be left to a human.
func (c *Core) backfillPatches(zohoID string, oc *entity.CheckoutParams) ([]entity.OrderedItemPatch, error) {
	record, err := c.zoho.GetOrder(zohoID)
	if err != nil {
		return nil, fmt.Errorf("read zoho order: %w", err)
	}

	want := c.allOrderedItems(oc)
	if len(want) != len(record.OrderedItems) {
		return nil, fmt.Errorf("subform has %d rows, OpenCart has %d: edited in Zoho",
			len(record.OrderedItems), len(want))
	}

	patches := make([]entity.OrderedItemPatch, 0, len(want))
	for i, row := range record.OrderedItems {
		expected := want[i]

		// Rows come back in subform order, the order they were created in. Verify rather than
		// assume: a reordered or substituted product means the record was edited.
		if row.Product.ID != expected.Product.ID {
			return nil, fmt.Errorf("row %d holds product %s, OpenCart has %s: edited in Zoho",
				i+1, row.Product.ID, expected.Product.ID)
		}
		if int64(row.Quantity) != expected.Quantity {
			return nil, fmt.Errorf("row %d holds qty %v, OpenCart has %d: edited in Zoho",
				i+1, row.Quantity, expected.Quantity)
		}

		if math.Abs(row.DiscountP-expected.DiscountP) < discountEpsilon &&
			math.Abs(row.ListPrice-expected.ListPrice) < listPriceEpsilon {
			continue
		}

		patches = append(patches, entity.OrderedItemPatch{
			ID:        row.ID,
			Product:   entity.ZohoProduct{ID: expected.Product.ID},
			Quantity:  expected.Quantity,
			ListPrice: expected.ListPrice,
			DiscountP: expected.DiscountP,
			Total:     expected.Total,
		})
	}

	// Nothing is corrected piecemeal: either every drifted row goes, or none does.
	if len(patches) == 0 {
		return nil, nil
	}

	c.log.With(
		sl.Module("backfill"),
		slog.Int64("order_id", oc.OrderId),
		slog.String("zoho_id", zohoID),
		slog.Float64("discount_was", record.OrderedItems[0].DiscountP),
		slog.Float64("discount_now", want[0].DiscountP),
		slog.Float64("zoho_total_was", round2(record.GrandTotal)),
		slog.Float64("charged", round2(oc.Total)),
	).Info("discount drift")

	return patches, nil
}

// allOrderedItems rebuilds the complete subform for an order — buildZohoOrder splits it into the
// first chunk plus the overflow chunks, and the backfill needs it whole and in order.
func (c *Core) allOrderedItems(oc *entity.CheckoutParams) []entity.OrderedItem {
	zo, chunks := c.buildZohoOrder(oc, "")

	items := make([]entity.OrderedItem, 0, len(zo.OrderedItems))
	items = append(items, zo.OrderedItems...)
	for _, chunk := range chunks {
		for _, item := range chunk {
			items = append(items, *item)
		}
	}

	return items
}
