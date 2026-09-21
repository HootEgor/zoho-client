package core

import (
	"log/slog"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
)

// notifyTranzzo tells the UA shop's OpenCart module what a Zoho manager just did to an order, by
// enqueueing a zoho_order task. It is the outbound half of that shop's payment exchange: the
// inbound half reads oc_order.wf_payment_*, this half is the only way anything travels back.
//
// Called after the webhook has been applied to OpenCart, never before: the module reacts by
// reading the order's own basket, so it must see the corrected order rather than the old one.
//
// Nothing here is fatal. A queue row that fails to insert costs the shop a re-issued payment link,
// not the order update that was already committed, and returning an error would turn a successful
// update into a 500 that Zoho would retry — re-applying an update that already landed.
func (c *Core) notifyTranzzo(log *slog.Logger, orderId int64, event entity.TranzzoOrderEvent) {
	// Inert on every other shop: oc_tranzzo_queue exists only where that module is installed.
	if !c.site.TranzzoPayments() {
		return
	}

	log = log.With(
		slog.String("root_code", entity.TranzzoRootCode(orderId)),
		slog.String("status", event.Status),
		slog.Bool("cancel", event.Cancel),
		slog.Bool("zoho_managed", event.ZohoManaged),
	)

	if !event.Valid() {
		// The module would file this as "no_status" and do nothing. Saying so here names the
		// order; from its journal alone it would only be one more rejected row.
		log.Warn("tranzzo event carries neither a status nor a flag, not queued")
		return
	}

	// Enqueueing is a write to the shop, and the module acts on it with real money — capturing a
	// hold, voiding one, issuing a new payment link. Dry-run reports it instead.
	if c.dryRun {
		log.With(slog.Float64("sum", event.Sum)).
			Warn("DRY RUN: tranzzo queue task not written")
		return
	}

	if err := c.repo.EnqueueTranzzoOrderEvent(orderId, event); err != nil {
		log.With(sl.Err(err)).Error("enqueue tranzzo order event")
		return
	}

	log.With(slog.Float64("sum", event.Sum)).Info("tranzzo queue task written")
}

// tranzzoOrderEvent describes an applied webhook to the shop's module.
//
// A cancellation is sent with the cancel flag as well as the status. The module honours the flag
// over its status list — the flag "пришёл явным решением Zoho", the list is phrase matching — so a
// cancellation still releases the hold even if the module's admin wording has drifted from this
// shop's Zoho picklist. The capture side has no such flag and depends on that wording agreeing.
func (c *Core) tranzzoOrderEvent(statusId int, statusName, zohoId string, total float64) entity.TranzzoOrderEvent {
	return entity.TranzzoOrderEvent{
		Status: statusName,
		Sum:    round2(total),
		Cancel: statusId == c.site.StatusCanceled,
		ZohoID: zohoId,
	}
}
