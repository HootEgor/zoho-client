package entity

import (
	"encoding/json"
	"fmt"
	"net/http"
	"zohoclient/internal/lib/validate"
)

// ApiPaymentUpdate is the payload of POST /zoho/webhook/payment: one Sales Order and the whole
// Payments list Zoho currently holds for it. Zoho sends the list, not a delta, so a payment this
// service never created (a manager's manual record, a second attempt) arrives alongside ours.
//
// Nothing here reaches OpenCart yet — the payload is recorded so the fields worth transferring
// can be chosen from what shops actually send.
type ApiPaymentUpdate struct {
	// ZohoID is the Sales Order id the payments belong to, not a payment id.
	ZohoID   string       `json:"zoho_id" validate:"required"`
	Payments []ApiPayment `json:"payments" validate:"dive"`

	// Raw is the decoded "data" object re-marshalled, so recording keeps every field Zoho sent
	// including the ones this struct does not name. Key order is not preserved; values are,
	// large ids included — the envelope decodes numbers with UseNumber.
	Raw json.RawMessage `json:"-"`
}

// ApiPayment is one record of the Zoho Payments module as the webhook reports it. Zoho sends the
// full record, `$`-prefixed system fields and lookup objects included; only the fields that could
// plausibly matter to OpenCart are named here, the rest survive in ApiPaymentUpdate.Raw.
type ApiPayment struct {
	// ZohoID is the Payments record id; OrderID is the Sales Order it is linked to and repeats
	// ApiPaymentUpdate.ZohoID.
	ZohoID  string `json:"zoho_id" validate:"required"`
	OrderID string `json:"order_id"`

	Name         string  `json:"Name"`
	Status       string  `json:"Status"`
	Sum          float64 `json:"Sum"`
	Currency     string  `json:"Currency"`
	ExchangeRate float64 `json:"Exchange_Rate"`

	// PaymentTime is the money's own timestamp. Note the field name differs from the
	// Payment_time this service writes when it creates a record (entity.ZohoPayment).
	PaymentTime  string `json:"payment_datetime"`
	CreatedTime  string `json:"Created_Time"`
	ModifiedTime string `json:"Modified_Time"`

	StripePaymentIntentID   string `json:"Stripe_PaymentIntent_ID"`
	StripeCheckoutSessionID string `json:"Stripe_Checkout_Session_ID"`

	PaymentError string `json:"payment_error"`
	PaymentLink  string `json:"paymentLink"`
	RRN          string `json:"rrn"`
}

// UnmarshalJSON reads zoho_id through zohoID so a numeric Sales Order id keeps all 18 of its
// digits, and keeps the payload itself for recording.
func (p *ApiPaymentUpdate) UnmarshalJSON(data []byte) error {
	type alias ApiPaymentUpdate
	aux := struct {
		ZohoID json.RawMessage `json:"zoho_id"`
		*alias
	}{alias: (*alias)(p)}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	id, err := zohoID(aux.ZohoID)
	if err != nil {
		return fmt.Errorf("payments zoho_id: %w", err)
	}
	p.ZohoID = id
	p.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// UnmarshalJSON reads both ids through zohoID: the UA site quotes the payment id but not the
// order id it belongs to.
func (p *ApiPayment) UnmarshalJSON(data []byte) error {
	type alias ApiPayment
	aux := struct {
		ZohoID  json.RawMessage `json:"zoho_id"`
		OrderID json.RawMessage `json:"order_id"`
		*alias
	}{alias: (*alias)(p)}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	id, err := zohoID(aux.ZohoID)
	if err != nil {
		return fmt.Errorf("payment zoho_id: %w", err)
	}
	p.ZohoID = id

	orderID, err := zohoID(aux.OrderID)
	if err != nil {
		return fmt.Errorf("payment order_id: %w", err)
	}
	p.OrderID = orderID
	return nil
}

func (p *ApiPaymentUpdate) Bind(_ *http.Request) error {
	return validate.Struct(p)
}
