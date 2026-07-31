package core

import (
	"io"
	"log/slog"
	"math"
	"testing"
	"zohoclient/entity"
)

func r2(v float64) float64 { return math.Round(v*100) / 100 }

func approx(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// zohoRecomputedGrand mimics how Zoho derives a Sales Order grand total:
//
//	line Total  = ListPrice x Quantity x (1 - DiscountP/100)   <- Zoho recomputes this itself;
//	                                                              the Total we send is ignored
//	Sub_Total   = sum(taxable line Total)
//	Grand_Total = Sub_Total x (1 + VAT/100) + non-taxable      <- Adjustment is not in the formula
//
// The shipping product is flagged non-taxable in Zoho, so it lands in the "Товары без ПДВ"
// bucket and is added on top of the taxed base rather than into it.
//
// This is the value that flows back to us on the reverse webhook.
func zohoRecomputedGrand(o entity.ZohoOrder) float64 {
	return r2(zohoNet(o)*(1+o.VAT/100) + zohoNonTaxable(o))
}

// zohoNet is Zoho's Sub_Total: the sum of the taxable line totals it computes for itself.
func zohoNet(o entity.ZohoOrder) float64 {
	var net float64
	for _, it := range o.OrderedItems {
		if it.Product.ID == testShippingZohoID {
			continue
		}
		net += zohoLineTotal(it)
	}
	return net
}

// zohoNonTaxable is Zoho's "Товары без ПДВ": the lines excluded from the VAT base.
func zohoNonTaxable(o entity.ZohoOrder) float64 {
	var sum float64
	for _, it := range o.OrderedItems {
		if it.Product.ID == testShippingZohoID {
			sum += zohoLineTotal(it)
		}
	}
	return sum
}

// zohoLineTotal reproduces Zoho's own line-total calculation, ignoring OrderedItem.Total.
func zohoLineTotal(it entity.OrderedItem) float64 {
	return it.ListPrice * float64(it.Quantity) * (1 - it.DiscountP/100)
}

// testShippingZohoID is the Zoho id of the non-taxable shipping product
// ("Організація перевезення замовлення").
const testShippingZohoID = "SHIP"

func newTestCore() *Core {
	return &Core{
		log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		statuses:           map[int]string{1: "Нове"},
		shippingItemZohoId: testShippingZohoID,
	}
}

func minimalClient() *entity.ClientDetails {
	return &entity.ClientDetails{FirstName: "T", LastName: "U", Country: "Poland", ZipCode: "00-001"}
}

// ocOrder builds a stored OpenCart order. Tax on the line is the per-unit VAT at list price,
// which is where VatRate reads the true 23% from.
func ocOrder(subTotal, taxValue, discount, coupon float64, couponTitle string, total, price, qty float64) *entity.CheckoutParams {
	return &entity.CheckoutParams{
		OrderId: 1, Currency: "PLN",
		SubTotal: subTotal, TaxValue: taxValue, Discount: discount,
		Coupon: coupon, CouponTitle: couponTitle, Total: total,
		ClientDetails: minimalClient(),
		LineItems: []*entity.LineItem{
			{Name: "P", ZohoId: "Z1", Price: price, Qty: qty, Tax: price * 0.23, Total: price * qty},
		},
	}
}

func withShipping(oc *entity.CheckoutParams, shipping float64) *entity.CheckoutParams {
	oc.Shipping = shipping
	return oc
}

// reductionCase is one order shape driven through the whole channel.
//
// "fixed" cases are what OpenCart produces once discounts reduce the taxable base (the state we
// are preparing for). "legacy" cases are what it produces today, charging VAT on discounted
// amounts — they must still sync coherently, so the migration needs no flag day.
var reductionCases = []struct {
	name        string
	subTotal    float64
	taxValue    float64
	discount    float64
	coupon      float64
	couponTitle string
	shipping    float64
	total       float64
	price       float64
	qty         float64
	wantDiscP   float64 // the single pre-tax line discount we send to Zoho
	legacy      bool    // OpenCart charged VAT on the undiscounted subtotal
}{
	{name: "plain - no reductions", subTotal: 1000, taxValue: 230, total: 1230, price: 100, qty: 10, wantDiscP: 0},
	{name: "coupon only", subTotal: 1000, taxValue: 207, coupon: -100, couponTitle: "SAVE", total: 1107, price: 100, qty: 10, wantDiscP: 10},
	{name: "discount only", subTotal: 1000, taxValue: 207, discount: 100, total: 1107, price: 100, qty: 10, wantDiscP: 10},
	{name: "coupon + discount", subTotal: 1000, taxValue: 186.3, coupon: -100, discount: 90, couponTitle: "SAVE", total: 996.3, price: 100, qty: 10, wantDiscP: 19},
	{
		// Order 16939 once OpenCart reduces the taxable base: a true 10% off 8 x 65.00 = 468.00.
		name: "order 16939 (fixed)", subTotal: 422.764, taxValue: 87.5121, coupon: -42.2764,
		couponTitle: "Kupon (dark-591B9EAB)", total: 468.00, price: 52.8455, qty: 8, wantDiscP: 10,
	},
	{
		// Order 16942 once OpenCart reduces the taxable base: 10% promo then 10% volume.
		name: "order 16942 (fixed)", subTotal: 2517.8832, taxValue: 469.0816, coupon: -251.7883, discount: 226.6095,
		couponTitle: "Kupon (dark-68BE1A7D)", total: 2508.5670, price: 33.130042105263156, qty: 76, wantDiscP: 19,
	},
	{
		// Order 16939 as OpenCart charges it TODAY (VAT on the undiscounted subtotal). Zoho must
		// still receive the amount actually charged, 477.72, with the VAT that amount contains.
		name: "order 16939 (legacy, OpenCart not yet fixed)", subTotal: 422.764, taxValue: 97.2357, coupon: -42.2764,
		couponTitle: "Kupon (dark-591B9EAB)", total: 477.7233, price: 52.8455, qty: 8, wantDiscP: 8.13, legacy: true,
	},
	{
		// Order 16942 as charged today.
		name: "order 16942 (legacy, OpenCart not yet fixed)", subTotal: 2517.8832, taxValue: 579.1131, coupon: -251.7883, discount: 284.5208,
		couponTitle: "Kupon (dark-68BE1A7D)", total: 2560.6872, price: 33.130042105263156, qty: 76, wantDiscP: 17.32, legacy: true,
	},

	// Shipping rides through untaxed on both sides: OpenCart adds it after tax, and in Zoho the
	// shipping product is flagged non-taxable, so it lands in "Товары без ПДВ" instead of the VAT
	// base. It must therefore leave the product discount alone — a shipped order and the same
	// order collected in person carry the same percentage.
	{
		name: "shipping, no reductions", subTotal: 300, taxValue: 69, shipping: 39.90,
		total: 408.90, price: 100, qty: 3, wantDiscP: 0,
	},
	{
		name: "shipping + coupon", subTotal: 300, taxValue: 62.10, coupon: -30, couponTitle: "SAVE",
		shipping: 39.90, total: 372.00, price: 100, qty: 3, wantDiscP: 10,
	},
	{
		name: "shipping + coupon + discount", subTotal: 1000, taxValue: 195.50, coupon: -100, discount: 50,
		couponTitle: "SAVE", shipping: 25, total: 1070.50, price: 100, qty: 10, wantDiscP: 15,
	},
	{
		// Untaxed shipping must not be mistaken for the VAT bug: the health check has to stay
		// silent here and still fire on a genuinely legacy order that also ships (below).
		name: "shipping + coupon (legacy, OpenCart not yet fixed)", subTotal: 300, taxValue: 69,
		coupon: -30, couponTitle: "SAVE", shipping: 39.90, total: 378.90, price: 100, qty: 3,
		wantDiscP: 8.13, legacy: true,
	},
}

// ---- Forward: OpenCart -> Zoho (buildZohoOrder) ----

func TestBuildZohoOrder_ReductionModel(t *testing.T) {
	core := newTestCore()
	for _, tt := range reductionCases {
		t.Run(tt.name, func(t *testing.T) {
			oc := ocOrder(tt.subTotal, tt.taxValue, tt.discount, tt.coupon, tt.couponTitle, tt.total, tt.price, tt.qty)
			oc.Shipping = tt.shipping
			zo, _ := core.buildZohoOrder(oc, "c1")

			if zo.VAT != 23 {
				t.Errorf("VAT = %v, want 23", zo.VAT)
			}
			// Nothing is applied after tax any more.
			if zo.Adjustment != 0 {
				t.Errorf("Adjustment = %v, want 0 (every reduction is pre-tax and lives in the lines)", zo.Adjustment)
			}
			if zo.Discount != 0 || zo.DiscountP != 0 {
				t.Errorf("order-level Discount/DiscountP must be 0, got %v/%v", zo.Discount, zo.DiscountP)
			}
			if got := zo.OrderedItems[0].DiscountP; !approx(got, tt.wantDiscP, 0.001) {
				t.Errorf("line DiscountP = %v, want %v", got, tt.wantDiscP)
			}
			assertDiscountPWireFormat(t, zo)

			// The health check flags legacy orders (VAT declared on undiscounted amounts) with
			// a positive gap, and must stay silent on healthy ones.
			gap := taxHealthGap(oc)
			if tt.legacy && gap <= 0.01 {
				t.Errorf("taxHealthGap = %.4f, want > 0.01 for a legacy order", gap)
			}
			if !tt.legacy && math.Abs(gap) > 0.01 {
				t.Errorf("taxHealthGap = %.4f, want ~0 for a healthy order", gap)
			}

			// The two invariants that matter:
			// 1. Zoho's grand total is what the customer was actually charged.
			if grand := zohoRecomputedGrand(zo); !approx(grand, tt.total, 0.01) {
				t.Errorf("Zoho grand total = %.4f, want OpenCart total %.4f (drift %.4f)", grand, tt.total, grand-tt.total)
			}
			// 2. The VAT Zoho records is the VAT actually contained in that amount.
			zohoVat := zohoNet(zo) * zo.VAT / 100
			if lawful := oc.LawfulTax(); !approx(zohoVat, lawful, 0.01) {
				t.Errorf("Zoho VAT = %.4f, want lawful VAT %.4f", zohoVat, lawful)
			}
		})
	}
}

// assertDiscountPWireFormat checks that every line DiscountP fits Zoho's Percent field, which
// rejects values with more than 2 decimal places (INVALID_DATA on Ordered_Items[n].DiscountP).
func assertDiscountPWireFormat(t *testing.T, zo entity.ZohoOrder) {
	t.Helper()
	for i, it := range zo.OrderedItems {
		if !approx(it.DiscountP, r2(it.DiscountP), 1e-9) {
			t.Errorf("Ordered_Items[%d].DiscountP = %v has more than 2 decimal places; Zoho rejects it", i, it.DiscountP)
		}
	}
}

// Order 16953: no reductions at all, and shipping is untaxed on both sides — OpenCart adds it
// after tax, Zoho holds it as a non-taxable line. So the products carry no discount whatsoever.
// (Until the shipping fix this order was sent with a phantom 2.65% discount, squeezed out of the
// product lines to pay for a shipping VAT Zoho never charged.)
func TestBuildZohoOrder_UntaxedShipping_Order16953(t *testing.T) {
	core := newTestCore()
	oc := &entity.CheckoutParams{
		OrderId: 16953, Currency: "PLN",
		SubTotal: 105.691, TaxValue: 24.3089, Shipping: 14.99, Total: 144.9899,
		ClientDetails: minimalClient(),
		LineItems: []*entity.LineItem{
			{Name: "P1", ZohoId: "Z1", Price: 52.8455, Qty: 1, Tax: 12.1545, Total: 52.8455},
			{Name: "P2", ZohoId: "Z2", Price: 52.8455, Qty: 1, Tax: 12.1545, Total: 52.8455},
		},
	}

	zo, _ := core.buildZohoOrder(oc, "c1")
	assertDiscountPWireFormat(t, zo)

	// Untaxed shipping is how this shop is configured, not the VAT bug — no warning.
	if gap := taxHealthGap(oc); math.Abs(gap) > 0.01 {
		t.Errorf("taxHealthGap = %.4f, want ~0: untaxed shipping must not trigger the tax warning", gap)
	}
	if got := zo.OrderedItems[0].DiscountP; !approx(got, 0, 0.001) {
		t.Errorf("line DiscountP = %v, want 0: untaxed shipping must not discount the products", got)
	}
	// The catalogue price, to within the sub-grosz noise of OpenCart's 4-decimal figures.
	if got := zo.OrderedItems[0].ListPrice; !approx(got, 52.8455, 0.001) {
		t.Errorf("ListPrice = %v, want the catalogue price 52.8455", got)
	}
	// The shipping line rides at face value, outside the VAT base.
	ship := zo.OrderedItems[len(zo.OrderedItems)-1]
	if ship.Product.ID != testShippingZohoID || !approx(ship.ListPrice, 14.99, 0.0001) || ship.DiscountP != 0 {
		t.Errorf("shipping line = %+v, want SHIP at 14.99 with no discount", ship)
	}
	if grand := zohoRecomputedGrand(zo); !approx(grand, oc.Total, 0.01) {
		t.Errorf("Zoho grand total = %.4f, want %.4f (drift %.4f)", grand, oc.Total, grand-oc.Total)
	}
}

// Order 17103 as it reached Zoho on 2026-07-31: a 21% Spanish order with a 10% coupon and DHL
// shipping. Before the fix the shipping VAT was squeezed into the products and the lines went out
// at 10.79%, so a 24.39 polish showed as 21.76 instead of 21.95.
func TestBuildZohoOrder_UntaxedShipping_Order17103(t *testing.T) {
	core := newTestCore()
	oc := &entity.CheckoutParams{
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

	zo, _ := core.buildZohoOrder(oc, "c1")
	assertDiscountPWireFormat(t, zo)

	if zo.VAT != 21 {
		t.Errorf("VAT = %v, want 21", zo.VAT)
	}
	if gap := taxHealthGap(oc); math.Abs(gap) > 0.01 {
		t.Errorf("taxHealthGap = %.4f, want ~0: this order is healthy", gap)
	}
	// The coupon is 10% of the subtotal and that is exactly what the lines must carry.
	if got := zo.OrderedItems[0].DiscountP; !approx(got, 10, 0.001) {
		t.Errorf("line DiscountP = %v, want 10 (the coupon, with no shipping VAT folded in)", got)
	}
	if got := r2(zohoLineTotal(zo.OrderedItems[0]) / 30); !approx(got, 21.95, 0.01) {
		t.Errorf("unit net = %.2f, want 21.95", got)
	}
	if grand := zohoRecomputedGrand(zo); !approx(grand, oc.Total, 0.01) {
		t.Errorf("Zoho grand total = %.4f, want %.4f (drift %.4f)", grand, oc.Total, grand-oc.Total)
	}
	// The VAT Zoho records is the VAT the charged amount really contains.
	if zohoVat := zohoNet(zo) * zo.VAT / 100; !approx(zohoVat, oc.LawfulTax(), 0.01) {
		t.Errorf("Zoho VAT = %.4f, want lawful VAT %.4f", zohoVat, oc.LawfulTax())
	}
}

func TestBuildOrderedItem_DiscountCombination(t *testing.T) {
	tests := []struct {
		name       string
		price      float64
		master     float64
		qty        float64
		discountP  float64
		wantList   float64
		wantDiscP  float64
		wantNetTot float64
	}{
		{"no discount", 100, 0, 1, 0, 100, 0, 100},
		{"order reduction only", 100, 0, 1, 10, 100, 10, 90},
		{"special price only", 80, 100, 1, 0, 100, 20, 80},
		{"special price + order reduction", 80, 100, 1, 10, 100, 28, 72},
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
		oc         *entity.CheckoutParams
		wantSub    int64
		wantTax    int64
		wantDisc   int64
		wantCoupon int64
		wantShip   int64
		wantTotal  int64
	}{
		{
			name:       "plain - no reductions",
			items:      []entity.ApiOrderedItem{{ZohoID: "Z1", Price: 100, Quantity: 10, Total: 1000}},
			grandTotal: 1230,
			oc:         ocOrder(1000, 230, 0, 0, "", 1230, 100, 10),
			wantSub:    100000, wantTax: 23000, wantDisc: 0, wantCoupon: 0, wantTotal: 123000,
		},
		{
			name:       "coupon reconstructed as negative order_total.coupon",
			items:      []entity.ApiOrderedItem{{ZohoID: "Z1", Price: 100, Quantity: 10, Total: 900}},
			grandTotal: 1107,
			oc:         ocOrder(1000, 207, 0, -100, "SAVE", 1107, 100, 10),
			wantSub:    100000, wantTax: 20700, wantDisc: 0, wantCoupon: -10000, wantTotal: 110700,
		},
		{
			name:       "discount reconstructed as negative order_total.discount",
			items:      []entity.ApiOrderedItem{{ZohoID: "Z1", Price: 100, Quantity: 10, Total: 900}},
			grandTotal: 1107,
			oc:         ocOrder(1000, 207, 100, 0, "", 1107, 100, 10),
			wantSub:    100000, wantTax: 20700, wantDisc: -10000, wantCoupon: 0, wantTotal: 110700,
		},
		{
			// The lines blend coupon and discount into one figure; the split comes from the
			// stored order's proportion (100 : 90).
			name:       "coupon + discount split by stored proportion",
			items:      []entity.ApiOrderedItem{{ZohoID: "Z1", Price: 100, Quantity: 10, Total: 810}},
			grandTotal: 996.3,
			oc:         ocOrder(1000, 186.3, 90, -100, "SAVE", 996.3, 100, 10),
			wantSub:    100000, wantTax: 18630, wantDisc: -9000, wantCoupon: -10000, wantTotal: 99630,
		},
		{
			// A manager raised the quantity in Zoho: totals recompute, the reduction rescales.
			name:       "manager raised quantity - totals recomputed",
			items:      []entity.ApiOrderedItem{{ZohoID: "Z1", Price: 100, Quantity: 12, Total: 1080}},
			grandTotal: 1328.4,
			oc:         ocOrder(1000, 207, 0, -100, "SAVE", 1107, 100, 10),
			wantSub:    120000, wantTax: 24840, wantDisc: 0, wantCoupon: -12000, wantTotal: 132840,
		},

		// Shipping arrives as its own subform line, flagged non-taxable in Zoho. It must land in
		// order_total.shipping untouched and stay out of the tax row — the mirror of the forward
		// direction, where it stays out of the discount.
		{
			name: "shipping, no reductions",
			items: []entity.ApiOrderedItem{
				{ZohoID: "Z1", Price: 100, Quantity: 10, Total: 1000},
				{ZohoID: testShippingZohoID, Price: 39.90, Quantity: 1, Total: 39.90},
			},
			grandTotal: 1269.90, // 1000 x 1.23 + 39.90
			oc:         withShipping(ocOrder(1000, 230, 0, 0, "", 1269.90, 100, 10), 39.90),
			wantSub:    100000, wantTax: 23000, wantDisc: 0, wantCoupon: 0, wantShip: 3990, wantTotal: 126990,
		},
		{
			name: "shipping + coupon - carriage is not discounted and not taxed",
			items: []entity.ApiOrderedItem{
				{ZohoID: "Z1", Price: 100, Quantity: 10, Total: 900},
				{ZohoID: testShippingZohoID, Price: 39.90, Quantity: 1, Total: 39.90},
			},
			grandTotal: 1146.90, // 900 x 1.23 + 39.90
			oc:         withShipping(ocOrder(1000, 207, 0, -100, "SAVE", 1146.90, 100, 10), 39.90),
			wantSub:    100000, wantTax: 20700, wantDisc: 0, wantCoupon: -10000, wantShip: 3990, wantTotal: 114690,
		},
		{
			// A manager dropped the carriage line in Zoho (free shipping). The shipping row follows
			// the payload, not the stored order, and must not leak into the coupon.
			name: "manager removed the shipping line",
			items: []entity.ApiOrderedItem{
				{ZohoID: "Z1", Price: 100, Quantity: 10, Total: 900},
			},
			grandTotal: 1107,
			oc:         withShipping(ocOrder(1000, 207, 0, -100, "SAVE", 1146.90, 100, 10), 39.90),
			wantSub:    100000, wantTax: 20700, wantDisc: 0, wantCoupon: -10000, wantShip: 0, wantTotal: 110700,
		},
		{
			// Zoho subforms can carry the same product twice; mergeItemsByZohoID folds them, and
			// the carriage must be counted by line total so both halves survive the merge.
			name: "carriage split across two lines",
			items: mergeItemsByZohoID([]entity.ApiOrderedItem{
				{ZohoID: "Z1", Price: 100, Quantity: 10, Total: 1000},
				{ZohoID: testShippingZohoID, Price: 20, Quantity: 1, Total: 20},
				{ZohoID: testShippingZohoID, Price: 19.90, Quantity: 1, Total: 19.90},
			}),
			grandTotal: 1269.90,
			oc:         withShipping(ocOrder(1000, 230, 0, 0, "", 1269.90, 100, 10), 39.90),
			wantSub:    100000, wantTax: 23000, wantDisc: 0, wantCoupon: 0, wantShip: 3990, wantTotal: 126990,
		},
	}

	core := newTestCore()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := core.computeReverseTotals(tt.items, tt.grandTotal, tt.oc)
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
			if got.Shipping != tt.wantShip {
				t.Errorf("Shipping = %d, want %d", got.Shipping, tt.wantShip)
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

// ---- Round trip: OpenCart -> Zoho -> OpenCart ----

// reversePayload maps a built Zoho order back to the webhook shape Zoho sends us, using the line
// totals Zoho computes for itself rather than the ones we sent.
func reversePayload(zo entity.ZohoOrder) []entity.ApiOrderedItem {
	items := make([]entity.ApiOrderedItem, 0, len(zo.OrderedItems))
	for _, it := range zo.OrderedItems {
		items = append(items, entity.ApiOrderedItem{
			ZohoID:   it.Product.ID,
			Price:    it.ListPrice,
			Quantity: int(it.Quantity),
			Total:    zohoLineTotal(it),
		})
	}
	return items
}

// The common case by far: Zoho pings a status change and the subform is untouched. UpdateOrder
// must recognise that and leave items and totals alone — otherwise every status ping restates
// the order, which is how order 16942's volume discount got wiped.
func TestStatusOnlyWebhookLeavesTotalsAlone(t *testing.T) {
	core := newTestCore()
	for _, tt := range reductionCases {
		t.Run(tt.name, func(t *testing.T) {
			oc := ocOrder(tt.subTotal, tt.taxValue, tt.discount, tt.coupon, tt.couponTitle, tt.total, tt.price, tt.qty)
			oc.Shipping = tt.shipping
			zo, _ := core.buildZohoOrder(oc, "c1")

			if !itemsUnchanged(reversePayload(zo), oc, core.shippingItemZohoId) {
				t.Error("subform round-tripped unchanged but itemsUnchanged() said otherwise; " +
					"a status-only webhook would rewrite this order's totals")
			}
		})
	}
}

// When a manager really does edit the subform, the totals are recomputed. On a healthy order
// that reproduces the stored rows; on a legacy one it normalises the order to the VAT actually
// contained in the price — which is unavoidable, since an inconsistent order cannot survive an
// edit unchanged.
func TestForwardReverseRoundTrip(t *testing.T) {
	core := newTestCore()
	for _, tt := range reductionCases {
		t.Run(tt.name, func(t *testing.T) {
			oc := ocOrder(tt.subTotal, tt.taxValue, tt.discount, tt.coupon, tt.couponTitle, tt.total, tt.price, tt.qty)
			oc.Shipping = tt.shipping

			zo, _ := core.buildZohoOrder(oc, "c1")
			grand := zohoRecomputedGrand(zo)
			got := core.computeReverseTotals(reversePayload(zo), grand, oc)

			// Always: the customer is charged exactly what they were charged before...
			if !approx(float64(got.Total)/100, tt.total, 0.01) {
				t.Errorf("round-trip total = %.2f, want %.2f (drift %.2f)",
					float64(got.Total)/100, tt.total, float64(got.Total)/100-tt.total)
			}
			// ...the tax row is the VAT that amount really contains...
			if !approx(float64(got.Tax)/100, oc.LawfulTax(), 0.01) {
				t.Errorf("round-trip tax = %.2f, want lawful VAT %.2f", float64(got.Tax)/100, oc.LawfulTax())
			}
			// ...the carriage comes back untouched, never absorbed into a reduction...
			if !approx(float64(got.Shipping)/100, tt.shipping, 0.01) {
				t.Errorf("round-trip shipping = %.2f, want %.2f", float64(got.Shipping)/100, tt.shipping)
			}
			// ...and the rows reconcile, exactly.
			if sum := got.ItemsTotal + got.Tax + got.Discount + got.Coupon + got.Shipping; sum != got.Total {
				t.Errorf("rows sum %d != total %d", sum, got.Total)
			}

			if tt.legacy {
				// The reductions are restated to their net equivalents (the post-tax portion is
				// grossed down by 1+rate). The total is untouched, so the customer is unaffected.
				return
			}

			// On a healthy order the reductions come back intact. They can land a grosz off:
			// OpenCart keeps 4 decimals while order_total rows are integer cents, and the rows
			// are derived so they sum to the total exactly — a sub-cent remainder must settle
			// somewhere.
			if !approx(float64(got.Coupon)/100, -math.Abs(tt.coupon), 0.011) {
				t.Errorf("round-trip coupon = %.2f, want %.2f", float64(got.Coupon)/100, -math.Abs(tt.coupon))
			}
			if !approx(float64(got.Discount)/100, -math.Abs(tt.discount), 0.011) {
				t.Errorf("round-trip discount = %.2f, want %.2f", float64(got.Discount)/100, -math.Abs(tt.discount))
			}
		})
	}
}
