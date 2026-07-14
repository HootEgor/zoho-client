package core

import (
	"math"
	"testing"
	"zohoclient/entity"
)

// r2 rounds to 2 decimals WITHOUT the sign-flipping that the production round2 helper
// applies, so negative values (e.g. Adjustment) compare correctly in tests.
func r2(v float64) float64 { return math.Round(v*100) / 100 }

func approx(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// zohoRecomputedGrand mimics how Zoho derives a Sales Order grand total: it sums the net
// line totals, applies VAT% to that base, then adds the (negative) post-tax Adjustment.
// This is the value that flows back to us on the reverse webhook.
func zohoRecomputedGrand(o entity.ZohoOrder) float64 {
	var net float64
	for _, it := range o.OrderedItems {
		net += it.Total
	}
	tax := r2(net * o.VAT / 100)
	return r2(net + tax + o.Adjustment)
}

func newTestCore() *Core {
	return &Core{
		statuses:           map[int]string{1: "Нове"},
		shippingItemZohoId: "SHIP",
	}
}

func minimalClient() *entity.ClientDetails {
	return &entity.ClientDetails{FirstName: "T", LastName: "U", Country: "Poland", ZipCode: "00-001"}
}

// ---- Forward: OpenCart -> Zoho (buildZohoOrder) ----

func TestBuildZohoOrder_DiscountModel(t *testing.T) {
	tests := []struct {
		name          string
		subTotal      float64
		taxValue      float64
		discount      float64 // order_total 'discount' (post-tax), positive
		coupon        float64 // order_total 'coupon' (pre-tax), OpenCart-negative
		couponTitle   string
		total         float64
		price         float64
		qty           float64
		wantVAT       float64
		wantAdjust    float64
		wantSubTotal  float64
		wantCoupon    float64
		wantLineTotal float64 // net line total Zoho receives
	}{
		{
			name:     "plain - no reductions",
			subTotal: 1000, taxValue: 230, total: 1230,
			price: 100, qty: 10,
			wantVAT: 23, wantAdjust: 0, wantSubTotal: 1000, wantCoupon: 0, wantLineTotal: 1000,
		},
		{
			name:     "post-tax discount (first-buy 10% of gross)",
			subTotal: 1000, taxValue: 230, discount: 123, total: 1107,
			price: 100, qty: 10,
			wantVAT: 23, wantAdjust: -123, wantSubTotal: 1000, wantCoupon: 0, wantLineTotal: 1000,
		},
		{
			name:     "pre-tax coupon",
			subTotal: 1000, taxValue: 207, coupon: -100, couponTitle: "SAVE", total: 1107,
			price: 100, qty: 10,
			wantVAT: 23, wantAdjust: 0, wantSubTotal: 1000, wantCoupon: 100, wantLineTotal: 900,
		},
		{
			// OpenCart taxed the full subtotal (tax_value 230 = 1000*23%), so the coupon is
			// applied after VAT and must go to Adjustment, leaving lines at list price.
			// This is the order #16939 shape.
			name:     "post-tax coupon (VAT on full subtotal)",
			subTotal: 1000, taxValue: 230, coupon: -100, couponTitle: "SAVE", total: 1130,
			price: 100, qty: 10,
			wantVAT: 23, wantAdjust: -100, wantSubTotal: 1000, wantCoupon: 100, wantLineTotal: 1000,
		},
	}

	core := newTestCore()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oc := &entity.CheckoutParams{
				OrderId:       1,
				Currency:      "PLN",
				SubTotal:      tt.subTotal,
				TaxValue:      tt.taxValue,
				Discount:      tt.discount,
				Coupon:        tt.coupon,
				CouponTitle:   tt.couponTitle,
				Total:         tt.total,
				ClientDetails: minimalClient(),
				LineItems: []*entity.LineItem{
					// Tax is the per-unit VAT at list price; CouponIsPreTax reads it to tell
					// which side of VAT the coupon sits on.
					{Name: "P", ZohoId: "Z1", Price: tt.price, Qty: tt.qty, Tax: tt.price * 0.23, Total: tt.price * tt.qty},
				},
			}

			zo, _ := core.buildZohoOrder(oc, "c1")

			if zo.VAT != tt.wantVAT {
				t.Errorf("VAT = %v, want %v", zo.VAT, tt.wantVAT)
			}
			if !approx(zo.Adjustment, tt.wantAdjust, 0.001) {
				t.Errorf("Adjustment = %v, want %v", zo.Adjustment, tt.wantAdjust)
			}
			if !approx(zo.SubTotal, tt.wantSubTotal, 0.001) {
				t.Errorf("SubTotal = %v, want %v", zo.SubTotal, tt.wantSubTotal)
			}
			if !approx(zo.CouponValue, tt.wantCoupon, 0.001) {
				t.Errorf("CouponValue = %v, want %v", zo.CouponValue, tt.wantCoupon)
			}
			if zo.Discount != 0 || zo.DiscountP != 0 {
				t.Errorf("order-level Discount/DiscountP must be 0, got %v/%v", zo.Discount, zo.DiscountP)
			}
			if len(zo.OrderedItems) != 1 || !approx(zo.OrderedItems[0].Total, tt.wantLineTotal, 0.001) {
				t.Errorf("line net total = %v, want %v", zo.OrderedItems[0].Total, tt.wantLineTotal)
			}

			// The key invariant: Zoho's recomputed grand total must equal OpenCart's total,
			// i.e. no drift introduced by how the discount is modeled.
			if got := zohoRecomputedGrand(zo); !approx(got, tt.total, 0.001) {
				t.Errorf("Zoho recomputed grand = %v, want OpenCart total %v (drift %v)", got, tt.total, got-tt.total)
			}
		})
	}
}

func TestBuildOrderedItem_DiscountCombination(t *testing.T) {
	tests := []struct {
		name       string
		price      float64
		master     float64
		qty        float64
		discountP  float64 // coupon percent
		wantList   float64
		wantDiscP  float64
		wantNetTot float64
	}{
		{"no discount", 100, 0, 1, 0, 100, 0, 100},
		{"coupon only", 100, 0, 1, 10, 100, 10, 90},
		{"special price only", 80, 100, 1, 0, 100, 20, 80},
		{"special + coupon", 80, 100, 1, 10, 100, 28, 72},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			li := &entity.LineItem{ZohoId: "Z", Price: tt.price, MasterPrice: tt.master, Qty: tt.qty}
			it := buildOrderedItem(li, tt.discountP)
			if !approx(it.ListPrice, tt.wantList, 0.001) {
				t.Errorf("ListPrice = %v, want %v", it.ListPrice, tt.wantList)
			}
			if !approx(it.DiscountP, tt.wantDiscP, 0.001) {
				t.Errorf("DiscountP = %v, want %v", it.DiscountP, tt.wantDiscP)
			}
			if !approx(it.Total, tt.wantNetTot, 0.001) {
				t.Errorf("Total(net) = %v, want %v", it.Total, tt.wantNetTot)
			}
		})
	}
}

// ---- Reverse: Zoho -> OpenCart (computeReverseTotals) ----

func TestComputeReverseTotals(t *testing.T) {
	tests := []struct {
		name       string
		items      []entity.ApiOrderedItem
		grandTotal float64
		coupon     string
		taxRate    float64
		wantSub    int64
		wantTax    int64
		wantDisc   int64
		wantCoupon int64
		wantTotal  int64
	}{
		{
			name:       "plain - no reductions",
			items:      []entity.ApiOrderedItem{{ZohoID: "Z1", Price: 100, Quantity: 10, Total: 1000}},
			grandTotal: 1230, coupon: "", taxRate: 0.23,
			wantSub: 100000, wantTax: 23000, wantDisc: 0, wantCoupon: 0, wantTotal: 123000,
		},
		{
			name:       "post-tax discount reconstructed as negative order_total.discount",
			items:      []entity.ApiOrderedItem{{ZohoID: "Z1", Price: 100, Quantity: 10, Total: 1000}},
			grandTotal: 1107, coupon: "", taxRate: 0.23,
			wantSub: 100000, wantTax: 23000, wantDisc: -12300, wantCoupon: 0, wantTotal: 110700,
		},
		{
			name:       "pre-tax coupon reconstructed as negative order_total.coupon",
			items:      []entity.ApiOrderedItem{{ZohoID: "Z1", Price: 100, Quantity: 10, Total: 900}},
			grandTotal: 1107, coupon: "SAVE", taxRate: 0.23,
			wantSub: 100000, wantTax: 20700, wantDisc: 0, wantCoupon: -10000, wantTotal: 110700,
		},
		{
			// Lines arrive at full price (Total == Price*Qty) despite a coupon code: this is a
			// post-tax coupon. VAT is on the full base and the coupon is the gross gap.
			name:       "post-tax coupon reconstructed as negative order_total.coupon",
			items:      []entity.ApiOrderedItem{{ZohoID: "Z1", Price: 100, Quantity: 10, Total: 1000}},
			grandTotal: 1130, coupon: "SAVE", taxRate: 0.23,
			wantSub: 100000, wantTax: 23000, wantDisc: 0, wantCoupon: -10000, wantTotal: 113000,
		},
	}

	core := newTestCore()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := core.computeReverseTotals(tt.items, tt.grandTotal, tt.coupon, tt.taxRate)
			if got.ItemsTotal != tt.wantSub {
				t.Errorf("ItemsTotal = %d, want %d", got.ItemsTotal, tt.wantSub)
			}
			if got.Tax != tt.wantTax {
				t.Errorf("Tax = %d, want %d", got.Tax, tt.wantTax)
			}
			if got.Discount != tt.wantDisc {
				t.Errorf("Discount = %d, want %d", got.Discount, tt.wantDisc)
			}
			if got.Coupon != tt.wantCoupon {
				t.Errorf("Coupon = %d, want %d", got.Coupon, tt.wantCoupon)
			}
			if got.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", got.Total, tt.wantTotal)
			}
			// order_total rows must always sum to the grand total.
			if sum := got.ItemsTotal + got.Tax + got.Discount + got.Coupon + got.Shipping; sum != got.Total {
				t.Errorf("rows sum %d != total %d", sum, got.Total)
			}
		})
	}
}

// ---- Round trip: OpenCart -> Zoho -> OpenCart preserves the discount and the total ----

func TestForwardReverseRoundTrip(t *testing.T) {
	tests := []struct {
		name        string
		subTotal    float64
		taxValue    float64
		discount    float64
		coupon      float64
		couponTitle string
		total       float64
		price       float64
		qty         float64
		wantDisc    int64 // expected reconstructed order_total.discount (negative)
		wantCoupon  int64 // expected reconstructed order_total.coupon (negative)
	}{
		{name: "plain", subTotal: 1000, taxValue: 230, total: 1230, price: 100, qty: 10, wantDisc: 0, wantCoupon: 0},
		{name: "post-tax discount", subTotal: 1000, taxValue: 230, discount: 123, total: 1107, price: 100, qty: 10, wantDisc: -12300, wantCoupon: 0},
		{name: "pre-tax coupon", subTotal: 1000, taxValue: 207, coupon: -100, couponTitle: "SAVE", total: 1107, price: 100, qty: 10, wantDisc: 0, wantCoupon: -10000},
		{name: "post-tax coupon", subTotal: 1000, taxValue: 230, coupon: -100, couponTitle: "SAVE", total: 1130, price: 100, qty: 10, wantDisc: 0, wantCoupon: -10000},
	}

	core := newTestCore()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oc := &entity.CheckoutParams{
				OrderId: 1, Currency: "PLN",
				SubTotal: tt.subTotal, TaxValue: tt.taxValue, Discount: tt.discount,
				Coupon: tt.coupon, CouponTitle: tt.couponTitle, Total: tt.total,
				ClientDetails: minimalClient(),
				LineItems: []*entity.LineItem{
					{Name: "P", ZohoId: "Z1", Price: tt.price, Qty: tt.qty, Tax: tt.price * 0.23, Total: tt.price * tt.qty},
				},
			}

			// Forward, then take the VAT rate the way the order's stored totals express it.
			taxRate := oc.TaxRate() / 100
			zo, _ := core.buildZohoOrder(oc, "c1")

			// Simulate Zoho recompute + the middleware mapping back to a reverse payload.
			grand := zohoRecomputedGrand(zo)
			reverseItems := make([]entity.ApiOrderedItem, 0, len(zo.OrderedItems))
			for _, it := range zo.OrderedItems {
				reverseItems = append(reverseItems, entity.ApiOrderedItem{
					ZohoID:   it.Product.ID,
					Price:    it.ListPrice,
					Quantity: int(it.Quantity),
					Total:    it.Total,
				})
			}

			got := core.computeReverseTotals(reverseItems, grand, zo.CouponTitle, taxRate)

			if got.Discount != tt.wantDisc {
				t.Errorf("round-trip Discount = %d, want %d", got.Discount, tt.wantDisc)
			}
			if got.Coupon != tt.wantCoupon {
				t.Errorf("round-trip Coupon = %d, want %d", got.Coupon, tt.wantCoupon)
			}
			// Total must come back within a cent of the original OpenCart total.
			if !approx(float64(got.Total)/100, tt.total, 0.01) {
				t.Errorf("round-trip total = %.2f, want %.2f (drift %.2f)", float64(got.Total)/100, tt.total, float64(got.Total)/100-tt.total)
			}
			if sum := got.ItemsTotal + got.Tax + got.Discount + got.Coupon + got.Shipping; sum != got.Total {
				t.Errorf("rows sum %d != total %d", sum, got.Total)
			}
		})
	}
}
