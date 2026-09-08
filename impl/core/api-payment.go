package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"zohoclient/entity"
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
			slog.Bool("created_here", zohoPaymentId != "" && p.ZohoID == zohoPaymentId),
			slog.String("status", p.Status),
			slog.Float64("sum", round2(p.Sum)),
			slog.String("currency", p.Currency),
			slog.String("payment_time", p.PaymentTime),
			slog.String("stripe_payment_intent", p.StripePaymentIntentID),
			slog.String("payment_error", p.PaymentError),
		).Info("payment reported by Zoho")
	}

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
