package entity

import (
	"encoding/json"
	"testing"
)

// The two payload shapes are built from the real structs, so a renamed JSON tag breaks this test
// rather than silently emptying the summary an admin reads in Telegram.

func TestVersionSummary_OutboundZohoOrder(t *testing.T) {
	order := ZohoOrder{
		Subject:    "Order 4210",
		Status:     "Нове замовлення",
		Currency:   "PLN",
		GrandTotal: 929.74,
		OrderedItems: []OrderedItem{
			{Product: ZohoProduct{ID: "Z1"}, Quantity: 2},
			{Product: ZohoProduct{ID: "Z2"}, Quantity: 1},
			{Product: ZohoProduct{ID: "SHIP"}, Quantity: 1},
		},
	}
	payload, err := json.Marshal(order)
	if err != nil {
		t.Fatalf("marshal order: %v", err)
	}

	s, err := Version{ID: "0", Payload: string(payload)}.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if s.Source != VersionSourceZoho {
		t.Errorf("Source = %q, want %q", s.Source, VersionSourceZoho)
	}
	if s.Status != "Нове замовлення" {
		t.Errorf("Status = %q, want %q", s.Status, "Нове замовлення")
	}
	if !s.HasTotal || s.GrandTotal != 929.74 {
		t.Errorf("GrandTotal = %v (has=%v), want 929.74", s.GrandTotal, s.HasTotal)
	}
	if s.Currency != "PLN" {
		t.Errorf("Currency = %q, want PLN", s.Currency)
	}
	if s.Subject != "Order 4210" {
		t.Errorf("Subject = %q, want %q", s.Subject, "Order 4210")
	}
	// An outbound payload does not flag its shipping line, so all three items count as products.
	if s.Products != 3 || s.ShippingLines != 0 {
		t.Errorf("Products/ShippingLines = %d/%d, want 3/0", s.Products, s.ShippingLines)
	}
}

func TestVersionSummary_InboundApiOrder(t *testing.T) {
	order := ApiOrder{
		ZohoID:     "5000000123456",
		Status:     "Відправлено",
		GrandTotal: 1250.5,
		OrderedItems: []ApiOrderedItem{
			{ZohoID: "Z1", Quantity: 2},
			{ZohoID: "Z2", Quantity: 1},
			{ZohoID: "SHIP", Quantity: 1, Shipping: true},
		},
	}
	payload, err := json.Marshal(order)
	if err != nil {
		t.Fatalf("marshal order: %v", err)
	}

	s, err := Version{ID: "1", Payload: string(payload)}.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if s.Source != VersionSourceWebhook {
		t.Errorf("Source = %q, want %q", s.Source, VersionSourceWebhook)
	}
	if s.Status != "Відправлено" {
		t.Errorf("Status = %q, want %q", s.Status, "Відправлено")
	}
	if !s.HasTotal || s.GrandTotal != 1250.5 {
		t.Errorf("GrandTotal = %v (has=%v), want 1250.5", s.GrandTotal, s.HasTotal)
	}
	if s.ZohoID != "5000000123456" {
		t.Errorf("ZohoID = %q, want 5000000123456", s.ZohoID)
	}
	if s.Products != 2 || s.ShippingLines != 1 {
		t.Errorf("Products/ShippingLines = %d/%d, want 2/1", s.Products, s.ShippingLines)
	}
	// An inbound payload carries no currency; the summary must not invent one.
	if s.Currency != "" {
		t.Errorf("Currency = %q, want empty", s.Currency)
	}
}

func TestVersionSummary_EmptyAndBrokenPayloads(t *testing.T) {
	t.Run("no recognised fields", func(t *testing.T) {
		s, err := Version{Payload: `{"something_else":1}`}.Summary()
		if err != nil {
			t.Fatalf("Summary() error: %v", err)
		}
		if s.Source != "" || s.HasTotal || s.Products != 0 {
			t.Errorf("unexpected summary: %+v", s)
		}
	})

	t.Run("zero total is still a total", func(t *testing.T) {
		s, err := Version{Payload: `{"grand_total":0,"zoho_id":"X"}`}.Summary()
		if err != nil {
			t.Fatalf("Summary() error: %v", err)
		}
		if !s.HasTotal || s.GrandTotal != 0 {
			t.Errorf("GrandTotal = %v (has=%v), want 0 present", s.GrandTotal, s.HasTotal)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		if _, err := (Version{Payload: "not json"}).Summary(); err == nil {
			t.Error("expected an error for an undecodable payload")
		}
	})
}
