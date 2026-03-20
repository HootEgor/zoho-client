package entity

// Zoho CRM payment statuses (Ukrainian locale).
const (
	ZohoPaymentCreated       = "Створено"
	ZohoPaymentInProgress    = "В процесі"
	ZohoPaymentHeld          = "Кошти зарезервовано"
	ZohoPaymentPaid          = "Оплачено"
	ZohoPaymentCanceled      = "Скасовано"
	ZohoPaymentRefunded      = "Відшкодовано"
	ZohoPaymentError         = "Помилка операції"
)

// stripeToZohoPaymentStatus maps Stripe/wfsync payment status strings
// (as stored in OpenCart wf_payment_status) to Zoho CRM payment statuses.
var stripeToZohoPaymentStatus = map[string]string{
	// Initial / awaiting payment
	"pending":                 ZohoPaymentCreated,
	"open":                    ZohoPaymentCreated,
	"requires_payment_method": ZohoPaymentCreated,
	"requires_confirmation":   ZohoPaymentCreated,

	// Payment in progress
	"processing":      ZohoPaymentInProgress,
	"complete":        ZohoPaymentInProgress,
	"requires_action": ZohoPaymentInProgress,

	// Hold confirmed, awaiting capture
	"requires_capture": ZohoPaymentHeld,

	// Fully paid
	"paid":      ZohoPaymentPaid,
	"succeeded": ZohoPaymentPaid,

	// Canceled or expired
	"canceled": ZohoPaymentCanceled,
	"expired":  ZohoPaymentCanceled,

	// Refunded
	"refunded": ZohoPaymentRefunded,
}

// ConvertPaymentStatus converts a Stripe/wfsync payment status to the
// corresponding Zoho CRM payment status. Returns ZohoPaymentError for
// unrecognized statuses.
func ConvertPaymentStatus(stripeStatus string) string {
	if zoho, ok := stripeToZohoPaymentStatus[stripeStatus]; ok {
		return zoho
	}
	return ZohoPaymentError
}
