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

// tranzzoToPaymentKey maps the Tranzzo payment status strings the UA shop's OpenCart module
// writes into wf_payment_status onto the same logical payment states.
//
// Like stripeToPaymentKey this is a contract with the writer, not a per-site setting. These four
// are the module's whole vocabulary — its PAY_STATE_* constants in
// catalog/model/extension/payment/tranzzo_multi.php, declared there as "a dictionary for the
// zoho-client service, not for a human". A fifth value appearing here would land on
// PaymentKeyError and surface in Zoho as the error picklist value, so keep it complete.
//
// The columns are the same wf_payment_* family wfsync uses — only the writer and the words differ,
// which is why the vocabulary is chosen by site.payments.source rather than sniffed from the value.
var tranzzoToPaymentKey = map[string]string{
	// Link issued, no money yet. The module also files a FAILED attempt here, on the grounds that
	// it produced no money either — so a failure is reported to Zoho as "created", never as an
	// error. Only an unknown string means error.
	"init": PaymentKeyCreated,
	// Funds authorised and held, awaiting capture. The Stripe equivalent is requires_capture.
	"auth": PaymentKeyHeld,
	// Captured — the money has moved. Note the module then reports the CAPTURED amount in
	// wf_payment_amount, which may be less than the sum the link was issued for.
	"capture": PaymentKeyPaid,
	// Money returned. buildOrderPaymentState folds Tranzzo's "void" and "refund" transaction types
	// into this one state, so a refund after capture is indistinguishable here from an
	// authorisation released before it. Canceled is the reading that fits the commoner case; a
	// refund will not reach the Відшкодовано picklist value unless the module starts telling
	// the two apart.
	"void": PaymentKeyCanceled,
}

// PaymentStatusKey converts a Stripe/wfsync payment status to the corresponding logical payment
// state. Returns PaymentKeyError for unrecognized statuses.
func PaymentStatusKey(stripeStatus string) string {
	if key, ok := stripeToPaymentKey[stripeStatus]; ok {
		return key
	}
	return PaymentKeyError
}

// TranzzoPaymentStatusKey converts a Tranzzo payment status to the corresponding logical payment
// state. Returns PaymentKeyError for unrecognized statuses.
func TranzzoPaymentStatusKey(tranzzoStatus string) string {
	if key, ok := tranzzoToPaymentKey[tranzzoStatus]; ok {
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

// keyToTranzzoStatus is the reverse of tranzzoToPaymentKey: the value to write into
// oc_order.wf_payment_status once Zoho, not the shop, drives the payment. The module reads the
// column back and renders it in the order history, so it has to be one of its own four words.
//
// It is not a bijection, and cannot be. Tranzzo has four states where the logical vocabulary has
// seven, so three logical states collapse:
//   - in_progress has no Tranzzo equivalent; no money has moved, which is what init means.
//   - refunded and canceled are both "money returned", which the module already merges into void
//     when it writes the column itself (buildOrderPaymentState folds its void and refund types).
//   - error maps to init because that is where the module itself files a failed attempt — "денег
//     по ней всё равно нет". Writing a word the module does not know would render as nothing.
var keyToTranzzoStatus = map[string]string{
	PaymentKeyCreated:    "init",
	PaymentKeyInProgress: "init",
	PaymentKeyHeld:       "auth",
	PaymentKeyPaid:       "capture",
	PaymentKeyCanceled:   "void",
	PaymentKeyRefunded:   "void",
	PaymentKeyError:      "init",
}

// TranzzoStatusForKey converts a logical payment state to the Tranzzo status string the UA shop's
// module understands. Returns "" for an unknown state, which callers must treat as "do not write":
// an empty wf_payment_status means "no payment" to both sides, so writing one would erase a state
// rather than report it.
func TranzzoStatusForKey(key string) string {
	return keyToTranzzoStatus[key]
}

// PaymentKeyIsSettled reports whether a logical state means the payment is over — no more money
// will move through it. Used to pick the one active payment out of a Zoho Payments list: Zoho
// cancels the previous payment when a manager raises a new one, so the live one is the one that
// has not settled this way.
func PaymentKeyIsSettled(key string) bool {
	return key == PaymentKeyCanceled || key == PaymentKeyRefunded || key == PaymentKeyError
}
