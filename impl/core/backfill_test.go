package core

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/database/sql"
)

type backfillRepo struct {
	Repository
	orders []sql.SyncedOrder

	modifiedTimeCalls int
}

func (r *backfillRepo) OrdersSyncedBetween(time.Time, time.Time) ([]sql.SyncedOrder, error) {
	return r.orders, nil
}

func (r *backfillRepo) SetOrderZohoModifiedTime(int64, time.Time) error {
	r.modifiedTimeCalls++
	return nil
}

type backfillZoho struct {
	Zoho
	record *entity.ZohoOrderRecord

	updateCalls int
	updatedID   string
	updatedRows []entity.OrderedItemPatch
}

func (z *backfillZoho) GetOrder(string) (*entity.ZohoOrderRecord, error) { return z.record, nil }

func (z *backfillZoho) UpdateOrderItemRows(orderID string, rows []entity.OrderedItemPatch) (string, error) {
	z.updateCalls++
	z.updatedID = orderID
	z.updatedRows = rows
	return "2026-07-31T18:00:00+02:00", nil
}

func backfillCore(repo *backfillRepo, zoho *backfillZoho) *Core {
	return &Core{
		log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		repo:               repo,
		zoho:               zoho,
		statuses:           map[int]string{1: "Нове"},
		shippingItemZohoId: testShippingZohoID,
	}
}

// order17103 is the real order that exposed the bug: 21% VAT, a 10% coupon and DHL carriage.
func order17103() *entity.CheckoutParams {
	return &entity.CheckoutParams{
		OrderId: 17103, Currency: "PLN",
		SubTotal: 878.0474, TaxValue: 165.9510,
		Coupon: -87.8047, CouponTitle: "Kupon (dark-6B89EB81)",
		Shipping: 39.90, Total: 996.0936,
		ClientDetails: minimalClient(),
		LineItems: []*entity.LineItem{
			{Name: "Gel Polish", ZohoId: "Z1", Price: 24.3902, Qty: 30, Tax: 5.1219, Total: 731.706},
			{Name: "Builder Gel", ZohoId: "Z2", Price: 52.8455, Qty: 1, Tax: 11.0976, Total: 52.8455},
			{Name: "Flash", ZohoId: "Z3", Price: 48.7805, Qty: 1, Tax: 10.2439, Total: 48.7805},
			{Name: "Cat Eye", ZohoId: "Z4", Price: 44.7154, Qty: 1, Tax: 9.3902, Total: 44.7154},
		},
	}
}

// syncedWithBug reproduces the Zoho record an order got while buildZohoOrder still folded the
// shipping VAT into the product lines: discountP = 1 - (Total/(1+rate) - Shipping)/SubTotal.
func syncedWithBug(oc *entity.CheckoutParams, zohoID string) *entity.ZohoOrderRecord {
	rate := oc.VatRate()
	var discountP float64
	if oc.SubTotal > 0 {
		discountP = round4((1 - (oc.Total/(1+rate)-oc.Shipping)/oc.SubTotal) * 100)
	}

	rec := &entity.ZohoOrderRecord{ID: zohoID, Subject: fmt.Sprintf("Order #%d", oc.OrderId)}
	var net float64
	for i, li := range oc.LineItems {
		it := buildOrderedItem(li, discountP)
		net += it.ListPrice * float64(it.Quantity) * (1 - it.DiscountP/100)
		rec.OrderedItems = append(rec.OrderedItems, entity.ZohoOrderedItemRow{
			ID:        fmt.Sprintf("row-%d", i+1),
			Product:   it.Product,
			Quantity:  float64(it.Quantity),
			ListPrice: it.ListPrice,
			DiscountP: it.DiscountP,
			Total:     it.Total,
		})
	}
	if oc.Shipping > 0 {
		rec.OrderedItems = append(rec.OrderedItems, entity.ZohoOrderedItemRow{
			ID:        "row-ship",
			Product:   entity.ZohoProduct{ID: testShippingZohoID},
			Quantity:  1,
			ListPrice: oc.Shipping,
			Total:     oc.Shipping,
		})
	}
	rec.GrandTotal = r2(net*(1+rate) + oc.Shipping)

	return rec
}

// syncedCorrectly is the same record as the fixed code produces.
func syncedCorrectly(core *Core, oc *entity.CheckoutParams, zohoID string) *entity.ZohoOrderRecord {
	rec := &entity.ZohoOrderRecord{ID: zohoID, Subject: fmt.Sprintf("Order #%d", oc.OrderId)}
	for i, it := range core.allOrderedItems(oc) {
		rec.OrderedItems = append(rec.OrderedItems, entity.ZohoOrderedItemRow{
			ID:        fmt.Sprintf("row-%d", i+1),
			Product:   it.Product,
			Quantity:  float64(it.Quantity),
			ListPrice: it.ListPrice,
			DiscountP: it.DiscountP,
			Total:     it.Total,
		})
	}
	rec.GrandTotal = round2(oc.Total)

	return rec
}

func backfillDay() (time.Time, time.Time) {
	from := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	return from, from.AddDate(0, 0, 1)
}

// The record went out at 10.79% and Zoho's grand total is short by the shipping VAT. The backfill
// must rewrite the product rows to the true 10% and leave the carriage alone.
func TestBackfill_CorrectsShippingVatDrift(t *testing.T) {
	oc := order17103()
	repo := &backfillRepo{orders: []sql.SyncedOrder{{ZohoID: "ZO-1", Order: oc}}}
	zoho := &backfillZoho{record: syncedWithBug(oc, "ZO-1")}
	core := backfillCore(repo, zoho)

	// Precondition: the stored record really is the buggy one.
	if got := zoho.record.OrderedItems[0].DiscountP; !approx(got, 10.79, 0.001) {
		t.Fatalf("fixture DiscountP = %v, want the buggy 10.79", got)
	}
	if drift := round2(oc.Total) - zoho.record.GrandTotal; !approx(drift, oc.Shipping*oc.VatRate(), 0.01) {
		t.Fatalf("fixture grand total drift = %.2f, want Shipping x rate = %.2f", drift, oc.Shipping*oc.VatRate())
	}

	from, to := backfillDay()
	res, err := core.BackfillOrderDiscounts(from, to, true)
	if err != nil {
		t.Fatalf("BackfillOrderDiscounts() error = %v", err)
	}

	if res.Corrected != 1 || res.Scanned != 1 || res.Failed != 0 || res.Skipped != 0 {
		t.Errorf("result = %+v, want 1 scanned and 1 corrected", res)
	}
	if zoho.updateCalls != 1 || zoho.updatedID != "ZO-1" {
		t.Fatalf("update calls = %d on %q, want 1 on ZO-1", zoho.updateCalls, zoho.updatedID)
	}
	// The carriage is already right, so only the four product rows are touched.
	if len(zoho.updatedRows) != 4 {
		t.Fatalf("rows updated = %d, want 4 (the carriage must not be rewritten)", len(zoho.updatedRows))
	}
	for i, row := range zoho.updatedRows {
		if row.ID == "" {
			t.Errorf("row %d has no subform id — Zoho would append it as a new line", i)
		}
		if !approx(row.DiscountP, 10, 0.001) {
			t.Errorf("row %d DiscountP = %v, want 10", i, row.DiscountP)
		}
	}
	if got := zoho.updatedRows[0].ListPrice; !approx(got, 24.3902, 0.0001) {
		t.Errorf("row 1 List_Price = %v, want the catalogue 24.3902", got)
	}
	// Our own write must be recorded so the resulting webhook is recognised as an echo.
	if repo.modifiedTimeCalls != 1 {
		t.Errorf("zoho_modified_time written %d time(s), want 1", repo.modifiedTimeCalls)
	}
}

// A dry run reports the same drift and writes nothing.
func TestBackfill_DryRunWritesNothing(t *testing.T) {
	oc := order17103()
	repo := &backfillRepo{orders: []sql.SyncedOrder{{ZohoID: "ZO-1", Order: oc}}}
	zoho := &backfillZoho{record: syncedWithBug(oc, "ZO-1")}
	core := backfillCore(repo, zoho)

	from, to := backfillDay()
	res, err := core.BackfillOrderDiscounts(from, to, false)
	if err != nil {
		t.Fatalf("BackfillOrderDiscounts() error = %v", err)
	}

	if res.Corrected != 1 {
		t.Errorf("Corrected = %d, want 1 (reported, not written)", res.Corrected)
	}
	if zoho.updateCalls != 0 || repo.modifiedTimeCalls != 0 {
		t.Errorf("dry run wrote something: %d update(s), %d modified-time write(s)",
			zoho.updateCalls, repo.modifiedTimeCalls)
	}
}

// Running it twice must be a no-op the second time.
func TestBackfill_LeavesCorrectOrdersAlone(t *testing.T) {
	oc := order17103()
	core := backfillCore(&backfillRepo{}, &backfillZoho{})
	repo := &backfillRepo{orders: []sql.SyncedOrder{{ZohoID: "ZO-1", Order: oc}}}
	zoho := &backfillZoho{record: syncedCorrectly(core, oc, "ZO-1")}
	core = backfillCore(repo, zoho)

	from, to := backfillDay()
	res, err := core.BackfillOrderDiscounts(from, to, true)
	if err != nil {
		t.Fatalf("BackfillOrderDiscounts() error = %v", err)
	}

	if res.Unchanged != 1 || res.Corrected != 0 {
		t.Errorf("result = %+v, want 1 unchanged and 0 corrected", res)
	}
	if zoho.updateCalls != 0 {
		t.Errorf("update calls = %d on an already-correct order, want 0", zoho.updateCalls)
	}
}

// An order a manager has since edited in Zoho must be reported and left untouched: the rows no
// longer describe the OpenCart order, so rewriting them would discard their work.
func TestBackfill_SkipsEditedOrders(t *testing.T) {
	from, to := backfillDay()

	tests := []struct {
		name string
		edit func(rec *entity.ZohoOrderRecord)
	}{
		{"row added in Zoho", func(rec *entity.ZohoOrderRecord) {
			rec.OrderedItems = append(rec.OrderedItems, entity.ZohoOrderedItemRow{
				ID: "row-extra", Product: entity.ZohoProduct{ID: "GIFT"}, Quantity: 1, ListPrice: 10, Total: 10,
			})
		}},
		{"row removed in Zoho", func(rec *entity.ZohoOrderRecord) {
			rec.OrderedItems = rec.OrderedItems[1:]
		}},
		{"product swapped", func(rec *entity.ZohoOrderRecord) {
			rec.OrderedItems[1].Product.ID = "OTHER"
		}},
		{"quantity changed", func(rec *entity.ZohoOrderRecord) {
			rec.OrderedItems[0].Quantity = 29
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oc := order17103()
			rec := syncedWithBug(oc, "ZO-1")
			tt.edit(rec)

			repo := &backfillRepo{orders: []sql.SyncedOrder{{ZohoID: "ZO-1", Order: oc}}}
			zoho := &backfillZoho{record: rec}
			core := backfillCore(repo, zoho)

			res, err := core.BackfillOrderDiscounts(from, to, true)
			if err != nil {
				t.Fatalf("BackfillOrderDiscounts() error = %v", err)
			}

			if res.Skipped != 1 || res.Corrected != 0 {
				t.Errorf("result = %+v, want 1 skipped and 0 corrected", res)
			}
			if zoho.updateCalls != 0 {
				t.Errorf("update calls = %d on an edited order, want 0", zoho.updateCalls)
			}
		})
	}
}

// An order without carriage was never affected by the bug and must not be touched.
func TestBackfill_NoShippingIsAlreadyCorrect(t *testing.T) {
	oc := order17103()
	oc.Shipping = 0
	oc.Total = 956.1936 // the same order collected in person

	repo := &backfillRepo{orders: []sql.SyncedOrder{{ZohoID: "ZO-1", Order: oc}}}
	zoho := &backfillZoho{record: syncedWithBug(oc, "ZO-1")}
	core := backfillCore(repo, zoho)

	from, to := backfillDay()
	res, err := core.BackfillOrderDiscounts(from, to, true)
	if err != nil {
		t.Fatalf("BackfillOrderDiscounts() error = %v", err)
	}

	if res.Unchanged != 1 || zoho.updateCalls != 0 {
		t.Errorf("result = %+v with %d update(s), want it left alone", res, zoho.updateCalls)
	}
}
