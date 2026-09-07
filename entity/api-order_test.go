package entity

import (
	"encoding/json"
	"testing"
)

// The UA site's Zoho function sends the Sales Order id as a bare JSON number while the subform
// ids are quoted. Both must land on the same 18-digit string — decoding the number through
// float64 would turn 739178000064455061 into 739178000064455000 and resolve to no order at all.
func TestApiOrderUnmarshal_NumericZohoID(t *testing.T) {
	payload := `{"zoho_id":739178000064455061,"status":"Відправлено","grand_total":2722.51,
		"coupon":"CHILLAX10","ordered_items":[
			{"zoho_id":"739178000063933582","price":195,"total":175.5,"quantity":1},
			{"zoho_id":739178000036008614,"price":65,"total":117,"quantity":2}]}`

	var order ApiOrder
	if err := json.Unmarshal([]byte(payload), &order); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if order.ZohoID != "739178000064455061" {
		t.Errorf("ZohoID = %q, want %q", order.ZohoID, "739178000064455061")
	}
	if order.Status != "Відправлено" || order.Coupon != "CHILLAX10" || order.GrandTotal != 2722.51 {
		t.Errorf("other fields lost: status=%q coupon=%q total=%v",
			order.Status, order.Coupon, order.GrandTotal)
	}
	if len(order.OrderedItems) != 2 {
		t.Fatalf("items = %d, want 2", len(order.OrderedItems))
	}
	if order.OrderedItems[0].ZohoID != "739178000063933582" {
		t.Errorf("quoted item id = %q, want 739178000063933582", order.OrderedItems[0].ZohoID)
	}
	if order.OrderedItems[1].ZohoID != "739178000036008614" {
		t.Errorf("numeric item id = %q, want 739178000036008614", order.OrderedItems[1].ZohoID)
	}
	if order.OrderedItems[1].Quantity != 2 || order.OrderedItems[1].Price != 65 {
		t.Errorf("item fields lost: %+v", order.OrderedItems[1])
	}
}

func TestApiOrderUnmarshal_MissingAndBadZohoID(t *testing.T) {
	var order ApiOrder
	if err := json.Unmarshal([]byte(`{"status":"X"}`), &order); err != nil {
		t.Fatalf("absent zoho_id should decode (Bind rejects it later), got %v", err)
	}
	if order.ZohoID != "" {
		t.Errorf("ZohoID = %q, want empty", order.ZohoID)
	}

	if err := json.Unmarshal([]byte(`{"zoho_id":{"id":1}}`), &order); err == nil {
		t.Error("an object zoho_id should be rejected")
	}
}
