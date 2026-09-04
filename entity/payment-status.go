package entity

// Logical payment states. They name what a payment *is*, independent of how any particular Zoho
// org spells it in its Payments picklist — that spelling lives in zoho.payment_statuses and is
// resolved by config.SiteSettings.PaymentStatus.
const (
	PaymentKeyCreated    = "created"
	PaymentKeyInProgress = "in_progress"
	PaymentKeyHeld       = "held"
	PaymentKeyPaid       = "paid"
	PaymentKeyCanceled   = "canceled"
	PaymentKeyRefunded   = "refunded"
	PaymentKeyError      = "error"
)

// stripeToPaymentKey maps Stripe/wfsync payment status strings (as stored in the OpenCart
// wf_payment_status column) to logical payment states.
//
// This map is a contract with wfsync, not a per-site setting: every shop reads the same Stripe
// vocabulary. Keep it complete if wfsync's status values change.
//
// Stripe PaymentIntent statuses: https://docs.stripe.com/payments/paymentintents/lifecycle
// Stripe Checkout Session statuses: https://docs.stripe.com/api/checkout/sessions/object#checkout_session_object-status
var stripeToPaymentKey = map[string]string{
	// Initial / awaiting payment
	"pending":                 PaymentKeyCreated,
	"open":                    PaymentKeyCreated,
	"requires_payment_method": PaymentKeyCreated,
	"requires_confirmation":   PaymentKeyCreated,

	// Payment in progress
	"processing":      PaymentKeyInProgress,
	"complete":        PaymentKeyInProgress,
	"requires_action": PaymentKeyInProgress,

	// Hold confirmed, awaiting capture
	"requires_capture": PaymentKeyHeld,

	// Fully paid
	"paid":      PaymentKeyPaid,
	"succeeded": PaymentKeyPaid,

	// Canceled or expired
	"canceled": PaymentKeyCanceled,
	"expired":  PaymentKeyCanceled,

	// Refunded
	"refunded": PaymentKeyRefunded,
}

// PaymentStatusKey converts a Stripe/wfsync payment status to the corresponding logical payment
// state. Returns PaymentKeyError for unrecognized statuses.
func PaymentStatusKey(stripeStatus string) string {
	if key, ok := stripeToPaymentKey[stripeStatus]; ok {
		return key
	}
	return PaymentKeyError
}

// PaymentStatusKeys returns every logical payment state, so a configuration supplying the Zoho
// picklist for them can be checked for completeness at startup.
func PaymentStatusKeys() []string {
	return []string{
		PaymentKeyCreated,
		PaymentKeyInProgress,
		PaymentKeyHeld,
		PaymentKeyPaid,
		PaymentKeyCanceled,
		PaymentKeyRefunded,
		PaymentKeyError,
	}
}
