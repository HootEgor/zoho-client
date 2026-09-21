package entity

import "fmt"

// Queue contract with the UA shop's OpenCart Tranzzo module. The module has no HTTP endpoint for
// this (removed in its T-015): a row in oc_tranzzo_queue is the only way in. Its cron worker picks
// up status 'N' rows once a minute, claims them ('W'), and finishes them ('F'); a row we insert
// needs only these three columns, every other one has a database default.
//
// Consumed by ZohoStub::parseTask and Controller::taskZohoOrder in
// catalog/controller/extension/payment/tranzzo_multi.php.
const (
	// TranzzoQueueMethod is the module's ZohoStub::QUEUE_METHOD. It routes the row to the Zoho
	// handler and is part of the contract, not an internal handler name.
	TranzzoQueueMethod = "zoho_order"

	// TranzzoQueueStatusNew is the status a fresh task carries. We rely on the column default,
	// but the module's dedup and worker queries both key on it, so it is named here too.
	TranzzoQueueStatusNew = "N"
)

// TranzzoRootCode builds the queue's order reference. The module parses it back with
// /^order_(\d+)$/ (orderIdFromRootCode), and deliberately has no order field in the payload:
// "лишнее поле в контракте — лишний повод его не заполнить".
func TranzzoRootCode(orderId int64) string {
	return fmt.Sprintf("order_%d", orderId)
}

// TranzzoOrderEvent is the payload of a zoho_order task: what a Zoho manager did to an order, told
// to the shop so its payment module can react — re-issue a payment for a corrected sum, release a
// hold on a cancellation, or step aside entirely.
//
// Only the fields ZohoStub::parseTask reads are here. Status is matched as text against the
// module's own configured capture/cancel lists, NOT against zoho.order_status_map, so the two
// configurations have to agree on the wording; the module normalises case and spacing before
// comparing. Sum is in major units (the module converts with sumToMinor), unlike every other
// amount this service handles.
type TranzzoOrderEvent struct {
	// Status is the Zoho Sales Order status, spelled as the picklist spells it. Omitted when the
	// event carries a flag instead — a flag is a decision on its own, and parseTask only rejects
	// a payload ("no_status") when the status is empty AND no flag is set.
	Status string `json:"status,omitempty"`
	// Sum is the order's new grand total in major units. Optional: the module decides what to
	// charge from the order's own basket and uses this only to spot a divergence.
	Sum float64 `json:"sum,omitempty"`
	// Cancel overrides the status text match entirely (parseTask: the flag "пришёл явным решением
	// Zoho", the status list is only phrase matching), so a cancellation does not depend on the
	// module's admin list carrying the right words.
	Cancel bool `json:"cancel,omitempty"`
	// ZohoManaged hands payment control to Zoho: from then on the module neither captures, voids
	// nor re-issues, and instead reads oc_order.wf_payment_* — which this service writes. A
	// one-way latch on the module's side, and exempt from its duplicate check, so re-sending it
	// is harmless.
	ZohoManaged bool `json:"zoho_managed,omitempty"`
	// ZohoID is journal only. The module records it and never resolves anything by it — the order
	// comes from root_code.
	ZohoID string `json:"zoho_id,omitempty"`
}

// Valid reports whether the module would accept this payload. It mirrors parseTask's own rule: a
// status is required unless a flag carries the decision. An invalid event is still recorded in the
// module's journal with error "no_status", so sending one costs a row and achieves nothing.
func (e TranzzoOrderEvent) Valid() bool {
	return e.Status != "" || e.Cancel || e.ZohoManaged
}
