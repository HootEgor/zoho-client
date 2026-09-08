package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"zohoclient/entity"
	"zohoclient/internal/database/sql"
)

// paymentRepo answers the two reads UpdatePayments performs and counts them, so a test can show
// the endpoint writes nothing to OpenCart.
type paymentRepo struct {
	Repository
	orderId   int64
	order     *entity.CheckoutParams
	paymentId string
	searchErr error

	searchCalls int
}

func (r *paymentRepo) OrderSearchByZohoId(string) (int64, *entity.CheckoutParams, error) {
	r.searchCalls++
	if r.searchErr != nil {
		return 0, nil, r.searchErr
	}
	return r.orderId, r.order, nil
}

func (r *paymentRepo) GetOrderZohoPaymentId(int64) (string, error) {
	return r.paymentId, nil
}

// paymentMongo records what was handed to it, which is the whole product of this stage.
type paymentMongo struct {
	MongoRepository
	orderID  int64
	payloads []string
}

func (m *paymentMongo) SavePaymentUpdate(orderID int64, payload string) error {
	m.orderID = orderID
	m.payloads = append(m.payloads, payload)
	return nil
}

func paymentTestCore(repo *paymentRepo, mongo *paymentMongo, dryRun bool) *Core {
	core := pushTestCore(&fakeRepo{}, &fakeZoho{})
	core.repo = repo
	core.mongoRepo = mongo
	core.dryRun = dryRun
	return core
}

func paymentTestRepo() *paymentRepo {
	order := pushableOrder()
	order.PaymentStatus = "requires_capture"
	return &paymentRepo{
		orderId:   order.OrderId,
		order:     order,
		paymentId: "739178000065068174",
	}
}

func decodePaymentUpdate(t *testing.T, payload string) *entity.ApiPaymentUpdate {
	t.Helper()

	var update entity.ApiPaymentUpdate
	if err := json.Unmarshal([]byte(payload), &update); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return &update
}

const paymentsPayload = `{"zoho_id":739178000059413569,"payments":[{"zoho_id":"739178000065068174",` +
	`"order_id":739178000059413569,"Status":"Створено","Sum":2500.35,"Currency":"UAH",` +
	`"payment_datetime":"2026-09-08T13:29:04+02:00","rrn":null,"Update_1C":false}]}`

// The webhook's only effect is a recorded payload: the order is resolved, the payments are logged
// and the raw body lands in MongoDB under the OpenCart order id.
func TestUpdatePayments_RecordsThePayload(t *testing.T) {
	repo := paymentTestRepo()
	mongo := &paymentMongo{}
	core := paymentTestCore(repo, mongo, false)

	if err := core.UpdatePayments(decodePaymentUpdate(t, paymentsPayload)); err != nil {
		t.Fatalf("UpdatePayments() error = %v", err)
	}

	if len(mongo.payloads) != 1 {
		t.Fatalf("SavePaymentUpdate called %d time(s), want 1", len(mongo.payloads))
	}
	if mongo.orderID != repo.orderId {
		t.Errorf("recorded under order_id %d, want %d", mongo.orderID, repo.orderId)
	}
	// What was recorded is what arrived, not a re-rendering of the fields this service models.
	if !strings.Contains(mongo.payloads[0], `"Update_1C"`) {
		t.Errorf("recorded payload lost the fields the struct does not name: %s", mongo.payloads[0])
	}
	if repo.searchCalls != 1 {
		t.Errorf("OrderSearchByZohoId called %d time(s), want 1 — a payment record exists only "+
			"after the Sales Order was synced, so there is no zoho_id race to retry around",
			repo.searchCalls)
	}
}

// Recording is not a write to Zoho or to the shop, so dry-run keeps it: skipping it would leave
// the endpoint doing nothing at all.
func TestUpdatePayments_RecordsUnderDryRun(t *testing.T) {
	mongo := &paymentMongo{}
	core := paymentTestCore(paymentTestRepo(), mongo, true)

	if err := core.UpdatePayments(decodePaymentUpdate(t, paymentsPayload)); err != nil {
		t.Fatalf("UpdatePayments() error = %v", err)
	}
	if len(mongo.payloads) != 1 {
		t.Errorf("SavePaymentUpdate called %d time(s) in dry run, want 1", len(mongo.payloads))
	}
}

// A Sales Order this shop never synced has nothing to file the payload under. Answering with an
// error would only have Zoho redeliver it forever.
func TestUpdatePayments_UnknownOrderIsAcknowledged(t *testing.T) {
	repo := paymentTestRepo()
	repo.searchErr = fmt.Errorf("order with zoho_id: %w", sql.ErrOrderNotFound)
	mongo := &paymentMongo{}
	core := paymentTestCore(repo, mongo, false)

	if err := core.UpdatePayments(decodePaymentUpdate(t, paymentsPayload)); err != nil {
		t.Fatalf("UpdatePayments() error = %v, want nil", err)
	}
	if len(mongo.payloads) != 0 {
		t.Errorf("SavePaymentUpdate called %d time(s) with no order to key it by, want 0",
			len(mongo.payloads))
	}
}

// A database that cannot answer is not the same thing, and must not read as a quiet success.
func TestUpdatePayments_LookupFailureIsReported(t *testing.T) {
	repo := paymentTestRepo()
	repo.searchErr = fmt.Errorf("connection refused")
	core := paymentTestCore(repo, &paymentMongo{}, false)

	if err := core.UpdatePayments(decodePaymentUpdate(t, paymentsPayload)); err == nil {
		t.Error("UpdatePayments() = nil, want an error when the lookup itself failed")
	}
}
