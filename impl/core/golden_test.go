package core

import (
	"encoding/json"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/config"
)

// update rewrites the golden file instead of comparing against it: `go test ./impl/core -update`.
var update = flag.Bool("update", false, "rewrite golden files")

// goldenOrder is a fixed OpenCart order exercising every branch buildZohoOrder has: a coupon, a
// per-line special price (MasterPrice > Price), non-taxable shipping, a NIP, a post terminal and
// a shipping method that resolves through the InPost keyword rules.
func goldenOrder() *entity.CheckoutParams {
	return &entity.CheckoutParams{
		OrderId:  17103,
		Currency: "PLN",
		SubTotal: 1000.00,
		TaxValue: 230.00,
		Coupon:   -100.00,
		Total:    1136.70,
		Shipping: 29.90,
		// The status the forward path stamps on the Sales Order.
		StatusId:       entity.OrderStatusNew,
		CouponTitle:    "Kupon (dark-591B9EAB)",
		Comment:        "leave at reception",
		ShippingCode:   "filterit1.filterit1",
		ShippingMethod: "InPost Paczkomat",
		PostTerminal:   "KRA01M",
		ClientDetails: &entity.ClientDetails{
			FirstName: "Jan",
			LastName:  "Kowalski",
			Email:     "jan@example.com",
			Phone:     "+48123456789",
			Country:   "Poland",
			Region:    "Malopolskie",
			City:      "Krakow",
			CityId:    4321,
			Street:    "Dluga 1/2",
			ZipCode:   "31-001",
			TaxId:     "1234567890",
		},
		LineItems: []*entity.LineItem{
			{Name: "A", Id: 1, Uid: "uid-1", ZohoId: "Z1", Price: 400.00, Qty: 2, Tax: 92.00, Total: 800.00, MasterPrice: 450.00},
			{Name: "B", Id: 2, Uid: "uid-2", ZohoId: "Z2", Price: 200.00, Qty: 1, Tax: 46.00, Total: 200.00},
		},
	}
}

// TestBuildZohoOrder_Golden pins the exact Sales Order payload produced for a fixed order under
// the default (site 1) settings. It exists so the site-configurability refactor can be proven not
// to change a single byte of what reaches Zoho.
func TestBuildZohoOrder_Golden(t *testing.T) {
	core := &Core{
		log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		site:               config.DefaultSiteSettings(),
		shippingItemZohoId: testShippingZohoID,
	}

	order, chunks := core.buildZohoOrder(goldenOrder(), "CONTACT1")
	if len(chunks) != 0 {
		t.Fatalf("unexpected chunks: %d", len(chunks))
	}

	// Due_Date is time.Now(): pin it so the golden file does not rot overnight.
	if order.DueDate != time.Now().Format("2006-01-02") {
		t.Errorf("DueDate = %q, want today", order.DueDate)
	}
	order.DueDate = "2026-01-01"

	got, err := json.MarshalIndent(order, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "zoho_order.golden.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run `go test ./impl/core -update` to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("buildZohoOrder payload changed.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
