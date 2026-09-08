package entity

import (
	"encoding/json"
	"testing"
)

// The payload one shop's Deluge function actually posts, trimmed of nothing: the Sales Order id
// arrives unquoted, the payment id quoted, and half the record is `$`-prefixed system fields and
// nulls this service does not model.
const paymentsWebhookSample = `{"zoho_id":739178000065138138,"payments":[{"zoho_id":"739178000065068174",` +
	`"order_id":739178000065138138,"Sells":{"name":"Замовлення № 136791 з інтернет-магазину",` +
	`"id":"739178000065138138"},"Owner":{"name":"Dark by Rior","id":"739178000000414001",` +
	`"email":"superadmin@darkbyrior.com"},"$currency_symbol":"₴","PDFlink":null,"$field_states":null,` +
	`"$review_process":{"approve":false,"reject":false,"resubmit":false},"Update_1C":false,` +
	`"paymentLink":null,"Name":"Payments #order-739178000065138138","Last_Activity_Time":null,` +
	`"$review":null,"$state":"save","Unsubscribed_Mode":null,"$process_flow":false,"Exchange_Rate":1,` +
	`"Stripe_PaymentIntent_ID":null,"Currency":"UAH","payment_datetime":"2026-09-08T13:29:04+02:00",` +
	`"$locked_for_me":false,"id":"739178000065068174","$approved":true,"Status":"Створено",` +
	`"Modified_Time":"2026-09-08T11:29:04+02:00","Created_Time":"2026-09-08T11:29:04+02:00",` +
	`"$editable":true,"Sum":2500.35,"Deal":null,"$orchestration":false,` +
	`"Contact":{"name":"Світлана Романюк","id":"739178000004170373"},"rrn":null,"field1":null,` +
	`"$in_merge":false,"Locked__s":false,"Stripe_Checkout_Session_ID":null,"payment_error":null,` +
	`"Tag":[],"$approval_state":"approved"}]}`

func TestApiPaymentUpdate_DecodesTheZohoPayload(t *testing.T) {
	var update ApiPaymentUpdate
	if err := json.Unmarshal([]byte(paymentsWebhookSample), &update); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	// The order id is 18 digits and arrives as a bare number: decoded through float64 it would
	// come back as 739178000065138000 and match no order.
	if got := update.ZohoID; got != "739178000065138138" {
		t.Errorf("zoho_id = %q, want 739178000065138138", got)
	}
	if len(update.Payments) != 1 {
		t.Fatalf("payments = %d, want 1", len(update.Payments))
	}

	p := update.Payments[0]
	if p.ZohoID != "739178000065068174" {
		t.Errorf("payment zoho_id = %q, want 739178000065068174", p.ZohoID)
	}
	if p.OrderID != "739178000065138138" {
		t.Errorf("payment order_id = %q, want 739178000065138138", p.OrderID)
	}
	if p.Status != "Створено" {
		t.Errorf("Status = %q, want Створено", p.Status)
	}
	if p.Sum != 2500.35 {
		t.Errorf("Sum = %v, want 2500.35", p.Sum)
	}
	if p.Currency != "UAH" {
		t.Errorf("Currency = %q, want UAH", p.Currency)
	}
	if p.ExchangeRate != 1 {
		t.Errorf("Exchange_Rate = %v, want 1", p.ExchangeRate)
	}
	if p.PaymentTime != "2026-09-08T13:29:04+02:00" {
		t.Errorf("payment_datetime = %q", p.PaymentTime)
	}
	if p.ModifiedTime != "2026-09-08T11:29:04+02:00" {
		t.Errorf("Modified_Time = %q", p.ModifiedTime)
	}
	if p.Name != "Payments #order-739178000065138138" {
		t.Errorf("Name = %q", p.Name)
	}
	// Every optional string in this record arrived as JSON null.
	if p.StripePaymentIntentID != "" || p.StripeCheckoutSessionID != "" ||
		p.PaymentError != "" || p.PaymentLink != "" || p.RRN != "" {
		t.Errorf("null fields did not decode to empty strings: %+v", p)
	}
}

// Recording is the whole point of this stage, so the payload has to survive decoding intact —
// including the fields the struct does not name.
func TestApiPaymentUpdate_KeepsTheRawPayload(t *testing.T) {
	var update ApiPaymentUpdate
	if err := json.Unmarshal([]byte(paymentsWebhookSample), &update); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(update.Raw, &raw); err != nil {
		t.Fatalf("Raw is not valid JSON: %v", err)
	}
	payments, ok := raw["payments"].([]interface{})
	if !ok || len(payments) != 1 {
		t.Fatalf("Raw carries no payments list: %v", raw["payments"])
	}
	record, _ := payments[0].(map[string]interface{})
	for _, field := range []string{"$approval_state", "Owner", "Contact", "Update_1C", "Tag"} {
		if _, ok := record[field]; !ok {
			t.Errorf("Raw dropped %q, which is the reason it exists", field)
		}
	}
}

// A payload with no zoho_id has no order to be filed under.
func TestApiPaymentUpdate_RequiresTheOrderId(t *testing.T) {
	var update ApiPaymentUpdate
	if err := json.Unmarshal([]byte(`{"payments":[]}`), &update); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if err := update.Bind(nil); err == nil {
		t.Error("Bind() accepted a payload with no zoho_id")
	}
}
