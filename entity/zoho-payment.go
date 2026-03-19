package entity

// ZohoPayment represents a payment record in the Zoho CRM "Payments" module.
// Linked to Sales_Orders via the Sells lookup field.
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
