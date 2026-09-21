package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"zohoclient/entity"
	"zohoclient/internal/config"
	"zohoclient/internal/database/sql"
	"zohoclient/internal/lib/sl"
)

// UpdatePayments handles an inbound payments webhook: Zoho reports the whole Payments list of one
// Sales Order whenever a record in it changes.
//
// This is the recording half of the feature. The payload is resolved to an OpenCart order, logged
// against what OpenCart already holds (the wfsync wf_payment_status and the payment record this
// service created) and stored in MongoDB. Nothing is written back to the shop yet — which fields
// are worth transferring is decided from the payloads real shops send.
func (c *Core) UpdatePayments(update *entity.ApiPaymentUpdate) error {
	log := c.log.With(
		sl.Module("core.UpdatePayments"),
		slog.String("zoho_id", update.ZohoID),
	)

	if update.ZohoID == "" {
		return fmt.Errorf("zoho_id is required")
	}

	// One lookup, no retry: unlike an order webhook this one cannot race our own zoho_id write —
	// a payment record exists only after the Sales Order it links to was created and recorded.
	orderId, order, err := c.repo.OrderSearchByZohoId(update.ZohoID)
	if err != nil {
		// Nothing is written to OpenCart here, so an unknown Sales Order is reported and
		// acknowledged rather than failed: a 500 would only have Zoho redeliver a payload that
		// has no order to be filed under. The handler has already logged the raw body.
		if errors.Is(err, sql.ErrOrderNotFound) {
			log.With(slog.Int("payments", len(update.Payments))).
				Warn("no order carries this zoho_id, payments not recorded")
			return nil
		}
		log.With(sl.Err(err)).Error("order lookup failed")
		return fmt.Errorf("order lookup failed: %w", err)
	}

	log = log.With(slog.Int64("order_id", orderId))

	// The payment record this service created for the order, so the log tells ours apart from one
	// a manager added in Zoho.
	zohoPaymentId, err := c.repo.GetOrderZohoPaymentId(orderId)
	if err != nil {
		log.With(sl.Err(err)).Warn("get zoho_payment_id")
	}

	for i := range update.Payments {
		p := &update.Payments[i]
		log.With(
			slog.String("payment_zoho_id", p.ZohoID),
			slog.Bool("created_here", createdHere(p, orderId, zohoPaymentId)),
			slog.String("status", p.Status),
			slog.Float64("sum", round2(p.Sum)),
			slog.String("currency", p.Currency),
			slog.String("payment_time", p.PaymentTime),
			slog.String("stripe_payment_intent", p.StripePaymentIntentID),
			slog.String("payment_error", p.PaymentError),
		).Info("payment reported by Zoho")
	}

	// A payment in the list that this service did not create was raised inside Zoho — a manager
	// charging a corrected order. That is the moment payment control passes to Zoho, so the shop's
	// module is told to stand down.
	c.detectZohoPaymentTakeover(log, orderId, zohoPaymentId, update)

	// Recording runs under dry-run too: it is the whole of what this endpoint does, and MongoDB is
	// this service's own store — not a write to Zoho or to the shop.
	c.savePaymentUpdateToMongo(orderId, update)

	log.With(
		slog.Int("payments", len(update.Payments)),
		slog.String("oc_payment_status", order.PaymentStatus),
		slog.String("zoho_payment_id", zohoPaymentId),
	).Info("payments update recorded")

	return nil
}

// savePaymentUpdateToMongo stores the webhook payload in the order's payment history. Failure is
// non-fatal: the payload is in the log either way.
func (c *Core) savePaymentUpdateToMongo(orderID int64, update *entity.ApiPaymentUpdate) {
	if c.mongoRepo == nil {
		return
	}

	// Raw is what arrived, fields this service does not model included; it is empty only if the
	// value was built rather than decoded, as a test does.
	payload := string(update.Raw)
	if payload == "" {
		payloadBytes, err := json.Marshal(update)
		if err != nil {
			c.log.With(sl.Err(err), slog.Int64("order_id", orderID)).
				Warn("failed to marshal payments payload for mongo")
			return
		}
		payload = string(payloadBytes)
	}

	if err := c.mongoRepo.SavePaymentUpdate(orderID, payload); err != nil {
		c.log.With(sl.Err(err), slog.Int64("order_id", orderID)).
			Warn("failed to save payments update to mongo")
	}
}

// createdHere reports whether a payment Zoho lists is the one this service created for the order.
//
// The recorded zoho_payment_id is the primary answer. The Name is a second, deliberate check for
// the window between CreatePayment returning an id and UpdateOrderZohoPayment storing it: a
// webhook arriving in between would otherwise show our own brand-new record as foreign, and on a
// tranzzo shop that reads as a takeover — which the module latches irreversibly. Recognising our
// own naming closes that window. A manager who names a record exactly "Payment #<order>" defeats
// it, which is a deliberate trade: a missed takeover is recoverable, a false one is not.
func createdHere(p *entity.ApiPayment, orderId int64, zohoPaymentId string) bool {
	if zohoPaymentId != "" && p.ZohoID == zohoPaymentId {
		return true
	}
	return p.Name == zohoPaymentName(orderId)
}

// detectZohoPaymentTakeover hands payment control to Zoho when the reported list holds a payment
// this service did not create.
//
// Sent on every such webhook rather than once. The module latches the flag itself (its UPDATE is
// guarded by zoho_managed = 0) and exempts these events from its duplicate check, so a repeat
// costs a queue row and changes nothing. It also earns one: after takeover, a queue task is the
// only moment the module looks at the order at all — applyZohoPaymentState() reads
// oc_order.wf_payment_* exactly there, with no polling in between — so each event we send is also
// what keeps the shop's own payment view current.
func (c *Core) detectZohoPaymentTakeover(log *slog.Logger, orderId int64, zohoPaymentId string, update *entity.ApiPaymentUpdate) {
	if !c.site.TranzzoPayments() {
		return
	}

	foreign := make([]string, 0, len(update.Payments))
	for i := range update.Payments {
		if p := &update.Payments[i]; !createdHere(p, orderId, zohoPaymentId) {
			foreign = append(foreign, p.ZohoID)
		}
	}
	if len(foreign) == 0 {
		return
	}

	log.With(slog.Any("payments_raised_in_zoho", foreign)).
		Info("payment initiated in Zoho, handing payment control over to Zoho")

	// Order matters: the module reads wf_payment_* while processing the queue task, so the state
	// has to be there before the task is. Writing after would have it read the previous value and
	// only catch up on the next event — or never, if none follows.
	c.writeZohoPaymentState(log, orderId, update)

	c.notifyTranzzo(log, orderId, entity.TranzzoOrderEvent{
		ZohoManaged: true,
		ZohoID:      update.ZohoID,
	})
}

// writeZohoPaymentState reflects the payment Zoho now drives back into oc_order.wf_payment_*.
//
// After a takeover the shop's module stops writing those columns and reads them instead, on the
// queue task alone — so this is the only thing keeping the shop's "Стан оплат" current, and the
// only reason the order history does not freeze at whatever the module last recorded.
//
// Nothing here is fatal: the payload is recorded either way, and the takeover event still goes out
// so the module at least learns it is no longer in charge.
func (c *Core) writeZohoPaymentState(log *slog.Logger, orderId int64, update *entity.ApiPaymentUpdate) {
	active := activePayment(c.site, update.Payments)
	if active == nil {
		log.Warn("no payment in the reported list carries a status this shop's picklist maps, " +
			"leaving wf_payment_* untouched")
		return
	}

	key := c.site.PaymentStatusKeyByName(active.Status)
	status := entity.TranzzoStatusForKey(key)
	if status == "" {
		// PaymentStatusKeyByName answered "" for an unlisted picklist value. Blanking the column
		// would read as "no payment" rather than "unknown", so the previous state stands.
		log.With(slog.String("zoho_status", active.Status)).
			Warn("payment status is not in zoho.payment_statuses, leaving wf_payment_* untouched")
		return
	}

	// The module stores the amount in minor units, and reads it back the same way.
	amountMinor := int64(math.Round(active.Sum * 100))

	log = log.With(
		slog.String("payment_zoho_id", active.ZohoID),
		slog.String("zoho_status", active.Status),
		slog.String("wf_payment_status", status),
		slog.Int64("wf_payment_amount", amountMinor),
	)

	if c.dryRun {
		log.Warn("DRY RUN: wf_payment_* not written")
		return
	}

	// wf_payment_id carries the Zoho Payments record id: a payment raised in Zoho has no Tranzzo
	// transaction behind it, and the module only displays this column in the order history.
	if err := c.repo.SetOrderPaymentState(orderId, status, active.ZohoID, amountMinor); err != nil {
		log.With(sl.Err(err)).Error("write payment state back to the shop")
		return
	}

	log.Info("payment state written back to the shop")
}

// activePayment picks the one payment of a Zoho list that is still live.
//
// Zoho holds a single active payment per Sales Order: raising a new one cancels the previous, so a
// takeover arrives as two webhooks — the cancellation, then the new payment. The live payment is
// therefore the last unsettled one, and a list where everything has settled is itself the answer:
// the order's payment really is cancelled, and the last settled record says so.
//
// "Last" is list order, which is the order Zoho reports its subform in. Payment_time is not used:
// a freshly raised payment may carry none at all.
func activePayment(site *config.SiteSettings, payments []entity.ApiPayment) *entity.ApiPayment {
	var live, settled *entity.ApiPayment

	for i := range payments {
		p := &payments[i]
		key := site.PaymentStatusKeyByName(p.Status)
		if key == "" {
			// Unmapped picklist value: it says nothing this shop can act on, and treating it as
			// live would hide a payment that is.
			continue
		}
		if entity.PaymentKeyIsSettled(key) {
			settled = p
			continue
		}
		live = p
	}

	if live != nil {
		return live
	}
	return settled
}
