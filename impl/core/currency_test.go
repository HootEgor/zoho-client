package core

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"zohoclient/entity"
)

// eurRate is oc_order.currency_value of test order 18313: EUR per PLN.
const eurRate = 0.23164971

// order18313 is shaped like the .com test order that reached Zoho in złoty labelled EUR: no VAT,
// three product lines and carriage, every amount stored in PLN.
func order18313(currency string, rate float64) *entity.CheckoutParams {
	return &entity.CheckoutParams{
		OrderId:       18313,
		Currency:      currency,
		CurrencyValue: rate,
		SubTotal:      182.12,
		Shipping:      14.99,
		Total:         197.11,
		StatusId:      entity.OrderStatusNew,
		ClientDetails: minimalClient(),
		LineItems: []*entity.LineItem{
			{Name: "A", ZohoId: "Z1", Price: 38.21, Qty: 2, Total: 76.42},
			{Name: "B", ZohoId: "Z2", Price: 52.85, Qty: 1, Total: 52.85},
			{Name: "C", ZohoId: "Z3", Price: 52.85, Qty: 1, Total: 52.85},
		},
	}
}

// goldenIn is the golden order (23% VAT, coupon, special price, shipping) in another currency.
func goldenIn(currency string, rate float64) *entity.CheckoutParams {
	oc := goldenOrder()
	oc.Currency = currency
	oc.CurrencyValue = rate
	return oc
}

// shippingListPrice returns the List_Price of the carriage line.
func shippingListPrice(zo entity.ZohoOrder) float64 {
	for _, it := range zo.OrderedItems {
		if it.Product.ID == testShippingZohoID {
			return it.ListPrice
		}
	}
	return 0
}

// An order placed in a currency other than the base reaches Zoho in that currency: OpenCart's
// base amounts multiplied by currency_value. A base-currency order is untouched.
func TestBuildZohoOrder_Currency(t *testing.T) {
	tests := []struct {
		name         string
		oc           *entity.CheckoutParams
		wantCurrency string
		wantGrand    float64
		wantShipping float64
		wantVAT      float64
	}{
		{"PLN rate 1, VAT, coupon, shipping", goldenIn("PLN", 1), "PLN", 1136.70, 29.90, 23},
		{"PLN rate missing", goldenIn("PLN", 0), "PLN", 1136.70, 29.90, 23},
		{"EUR VAT, coupon, shipping", goldenIn("EUR", eurRate), "EUR", 263.32, 6.9263, 23},
		{"EUR order 18313, VAT-free", order18313("EUR", eurRate), "EUR", 45.66, 3.4724, 0},
		{"PLN order 18313, VAT-free", order18313("PLN", 1), "PLN", 197.11, 14.99, 0},
	}
	core := newTestCore()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zo, _ := core.buildZohoOrder(tt.oc, "c1")

			if zo.Currency != tt.wantCurrency {
				t.Errorf("Currency = %q, want %q", zo.Currency, tt.wantCurrency)
			}
			if zo.GrandTotal != tt.wantGrand {
				t.Errorf("Grand_Total = %.2f, want %.2f", zo.GrandTotal, tt.wantGrand)
			}
			// What Zoho computes from the lines is what the payment link is raised for.
			if got := zohoRecomputedGrand(zo); !approx(got, tt.wantGrand, 0.005) {
				t.Errorf("Zoho-recomputed grand total = %.2f, want %.2f", got, tt.wantGrand)
			}
			if got := shippingListPrice(zo); !approx(got, tt.wantShipping, 0.0001) {
				t.Errorf("shipping List_Price = %.4f, want %.4f", got, tt.wantShipping)
			}
			if zo.VAT != tt.wantVAT {
				t.Errorf("VAT = %v, want %v", zo.VAT, tt.wantVAT)
			}
			if _, diverged := totalsDiverged(tt.oc.Total*tt.oc.ConversionRate(), zohoRecomputedGrand(zo)); diverged {
				t.Error("the order's own total diverges from what Zoho computes for it")
			}
		})
	}
}

// The conversion is a pure rescale: every line keeps its discount, and every price is the PLN
// one times the rate. The stored order itself is not modified.
func TestBuildZohoOrder_EURIsScaledPLN(t *testing.T) {
	core := newTestCore()
	eur := goldenIn("EUR", eurRate)
	pln, _ := core.buildZohoOrder(goldenIn("PLN", 1), "c1")
	got, _ := core.buildZohoOrder(eur, "c1")

	if len(got.OrderedItems) != len(pln.OrderedItems) {
		t.Fatalf("items = %d, want %d", len(got.OrderedItems), len(pln.OrderedItems))
	}
	for i := range pln.OrderedItems {
		p, e := pln.OrderedItems[i], got.OrderedItems[i]
		if e.DiscountP != p.DiscountP || e.Quantity != p.Quantity {
			t.Errorf("line %d: DiscountP/Qty = %v/%d, want %v/%d", i, e.DiscountP, e.Quantity, p.DiscountP, p.Quantity)
		}
		if !approx(e.ListPrice, p.ListPrice*eurRate, 0.0001) {
			t.Errorf("line %d: List_Price = %.4f, want %.4f", i, e.ListPrice, p.ListPrice*eurRate)
		}
	}
	if eur.Total != 1136.70 || eur.LineItems[0].Price != 400.00 {
		t.Error("buildZohoOrder modified the stored order's amounts")
	}
}

// A base-currency order is byte-for-byte what it was before currency conversion existed.
func TestBuildZohoOrder_PLNPayloadUnchanged(t *testing.T) {
	core := newTestCore()
	without, _ := core.buildZohoOrder(goldenIn("PLN", 0), "c1")
	with, _ := core.buildZohoOrder(goldenIn("PLN", 1), "c1")
	a, _ := json.Marshal(without)
	b, _ := json.Marshal(with)
	if !bytes.Equal(a, b) {
		t.Errorf("currency_value 1 changed the payload:\n%s\n%s", a, b)
	}
}

// Zoho reports the order in its own currency; OpenCart stores the base. Every amount written back
// is converted exactly once, and the status-only path compares like with like.
func TestUpdateOrder_CurrencyWriteBack(t *testing.T) {
	tests := []struct {
		name     string
		currency string
		rate     float64
	}{
		{"PLN", "PLN", 1},
		{"EUR", "EUR", eurRate},
	}
	for _, tt := range tests {
		t.Run(tt.name+" items changed", func(t *testing.T) {
			repo := webhookTestRepo()
			repo.order = order18313(tt.currency, tt.rate)
			core := webhookTestCore(repo, false)

			zo, _ := core.buildZohoOrder(repo.order, "c1")
			// The manager drops product C in Zoho.
			var kept []entity.OrderedItem
			for _, it := range zo.OrderedItems {
				if it.Product.ID != "Z3" {
					kept = append(kept, it)
				}
			}
			zo.OrderedItems = kept

			// Zoho reports its amounts to the cent of the order's currency.
			payload := reversePayload(zo)
			for i := range payload {
				payload[i].Price = r2(payload[i].Price)
				payload[i].Total = r2(payload[i].Total)
			}

			err := core.UpdateOrder(&entity.ApiOrder{
				ZohoID:       "739178000064455061",
				Status:       "Перевірка та збір",
				GrandTotal:   zohoRecomputedGrand(zo),
				OrderedItems: payload,
			})
			if err != nil {
				t.Fatalf("UpdateOrder() error = %v", err)
			}
			if repo.txCalls != 1 {
				t.Fatalf("UpdateOrderWithTransaction calls = %d, want 1", repo.txCalls)
			}

			tx := repo.lastTx
			// Zoho rounds its grand total to a cent of the order's currency, which converts back
			// to a coarser step of the base one: one euro cent is ~4.3 grosz.
			tol := int64(0.5/tt.rate) + 1
			within := func(got, want int64) bool { return got-want >= -tol && got-want <= tol }

			// 197.11 - 52.85, in PLN whatever the order's currency.
			if !within(tx.OrderTotal, 14426) {
				t.Errorf("oc_order.total = %s PLN, want 144.26", fmtCents(tx.OrderTotal))
			}
			if tx.Totals.Total != tx.OrderTotal {
				t.Errorf("order_total.total %d != oc_order.total %d", tx.Totals.Total, tx.OrderTotal)
			}
			tot := tx.Totals
			if sum := tot.SubTotal + tot.Shipping + tot.Tax + tot.Discount + tot.Coupon; sum != tot.Total {
				t.Errorf("order_total rows sum to %s, total is %s", fmtCents(sum), fmtCents(tot.Total))
			}
			if !within(tx.Totals.Shipping, 1499) {
				t.Errorf("shipping = %s PLN, want 14.99", fmtCents(tx.Totals.Shipping))
			}
			if tx.Totals.SubTotal != 12927 {
				t.Errorf("sub_total = %s PLN, want 129.27", fmtCents(tx.Totals.SubTotal))
			}
			for _, it := range tx.Items {
				if it.ZohoID == "Z1" && it.PriceInCents != 3821 {
					t.Errorf("Z1 price = %s PLN, want 38.21", fmtCents(it.PriceInCents))
				}
			}
		})

		t.Run(tt.name+" status only", func(t *testing.T) {
			repo := webhookTestRepo()
			repo.order = order18313(tt.currency, tt.rate)
			core := webhookTestCore(repo, false)
			var buf bytes.Buffer
			core.log = slog.New(slog.NewTextHandler(&buf, nil))

			zo, _ := core.buildZohoOrder(repo.order, "c1")
			err := core.UpdateOrder(&entity.ApiOrder{
				ZohoID:       "739178000064455061",
				Status:       "Перевірка та збір",
				GrandTotal:   zohoRecomputedGrand(zo),
				OrderedItems: reversePayload(zo),
			})
			if err != nil {
				t.Fatalf("UpdateOrder() error = %v", err)
			}
			if repo.txCalls != 0 || repo.statusCalls != 1 {
				t.Errorf("tx=%d status=%d, want a status-only update", repo.txCalls, repo.statusCalls)
			}
			if strings.Contains(buf.String(), "diverges") {
				t.Errorf("unchanged order reported as diverging:\n%s", buf.String())
			}
		})
	}
}

// A euro cent comes back as ~4.3 grosz, so Zoho's rounded grand total can land a few grosz below
// the lines on an order with no coupon or discount to absorb it. The rows must still reconcile.
func TestComputeReverseTotals_ForeignCurrencyRounding(t *testing.T) {
	core := newTestCore()
	oc := order18313("EUR", eurRate)
	items := []entity.ApiOrderedItem{
		{ZohoID: "Z1", Price: 38.21, Total: 76.42, Quantity: 2},
		{ZohoID: "Z2", Price: 52.85, Total: 52.85, Quantity: 1},
		{ZohoID: "Z3", Price: 52.85, Total: 52.85, Quantity: 1},
		{ZohoID: testShippingZohoID, Price: 14.99, Total: 14.99, Quantity: 1},
	}
	for _, short := range []float64{0, 0.01, 0.02, 0.04} {
		got := core.computeReverseTotals(items, 197.11-short, oc)
		if sum := got.ItemsTotal + got.Shipping + got.Tax + got.Discount + got.Coupon; sum != got.Total {
			t.Errorf("grand total %.2f short: rows sum to %s, total is %s",
				short, fmtCents(sum), fmtCents(got.Total))
		}
	}
}
