package entity

// ZohoPayment represents a payment record in the Zoho CRM custom "Payments" module.
// Linked to Sales_Orders via the Sells lookup field.
//
// Stripe fields store identifiers for payment reconciliation:
//   - StripePaymentIntentID: Stripe PaymentIntent ID (pi_xxx), from wf_payment_id column
//   - StripeCheckoutSessionID: Stripe Checkout Session ID (cs_xxx), from wf_payment_session_id column
//
// These are populated by the wfsync service which writes Stripe webhook data into OpenCart.
type ZohoPayment struct {
	Name                    string       `json:"Name"`
	Sells                   ZohoSellsRef `json:"Sells"`
	Status                  string       `json:"Status"`
	Sum                     float64      `json:"Sum"`
	Currency                string       `json:"Currency"`
	StripeCheckoutSessionID string       `json:"Stripe_Checkout_Session_ID,omitempty"`
	StripePaymentIntentID   string       `json:"Stripe_PaymentIntent_ID,omitempty"`
	PaymentTime             string       `json:"Payment_time,omitempty"`
	Email                   string       `json:"Email,omitempty"`
}

// ZohoSellsRef is a lookup reference to a Sales_Orders record.
type ZohoSellsRef struct {
	ID string `json:"id"`
}
