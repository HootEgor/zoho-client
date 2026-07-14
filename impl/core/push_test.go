package core

import (
	"io"
	"log/slog"
	"testing"
	"time"
	"zohoclient/entity"
)

// fakeRepo / fakeZoho embed their interfaces, so any method the push path is not supposed to
// call is nil and panics loudly rather than silently succeeding.
type fakeRepo struct {
	Repository
	zohoId string
	order  *entity.CheckoutParams

	changeZohoIdCalls int
	changedTo         string
}

func (f *fakeRepo) OrderSearchId(int64) (string, *entity.CheckoutParams, error) {
	return f.zohoId, f.order, nil
}

func (f *fakeRepo) ChangeOrderZohoId(_ int64, zohoId string) error {
	f.changeZohoIdCalls++
	f.changedTo = zohoId
	return nil
}

func (f *fakeRepo) SetOrderZohoModifiedTime(int64, time.Time) error { return nil }

func (f *fakeRepo) UpdateOrderZohoPayment(int64, string, string) error { return nil }

type fakeZoho struct {
	Zoho
	createOrderCalls   int
	updateOrderCalls   int
	updatedID          string
	createPaymentCalls int
}

func (f *fakeZoho) CreateContact(*entity.ClientDetails) (string, error) { return "contact-1", nil }

func (f *fakeZoho) CreateOrder(entity.ZohoOrder) (string, string, error) {
	f.createOrderCalls++
	return "NEW-ZOHO-ID", "2026-07-14T10:00:00+02:00", nil
}

func (f *fakeZoho) UpdateOrder(_ entity.ZohoOrder, id string) (string, error) {
	f.updateOrderCalls++
	f.updatedID = id
	return "2026-07-14T11:00:00+02:00", nil
}

func (f *fakeZoho) CreatePayment(entity.ZohoPayment) (string, error) {
	f.createPaymentCalls++
	return "PAY-1", nil
}

func pushableOrder() *entity.CheckoutParams {
	return &entity.CheckoutParams{
		OrderId: 16939, Currency: "PLN",
		SubTotal: 422.764, TaxValue: 87.5121, Coupon: -42.2764, Total: 468.00,
		CouponTitle:   "Kupon (dark-591B9EAB)",
		PaymentStatus: "complete", PaymentAmount: 46800,
		ClientDetails: minimalClient(),
		LineItems: []*entity.LineItem{
			{Name: "P", Id: 1, Uid: "uid-1", ZohoId: "Z1", Price: 52.8455, Qty: 8, Tax: 12.1545, Total: 422.764},
		},
	}
}

func pushTestCore(repo *fakeRepo, zoho *fakeZoho) *Core {
	return &Core{
		log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		repo:               repo,
		zoho:               zoho,
		statuses:           map[int]string{1: "Нове"},
		shippingItemZohoId: "SHIP",
	}
}

// A first push has no zoho_id: create the Sales Order and record its id.
func TestPushOrderToZoho_CreatesWhenNew(t *testing.T) {
	repo := &fakeRepo{zohoId: "", order: pushableOrder()}
	zoho := &fakeZoho{}
	core := pushTestCore(repo, zoho)

	zohoId, err := core.PushOrderToZoho(16939)
	if err != nil {
		t.Fatalf("PushOrderToZoho() error = %v", err)
	}

	if zohoId != "NEW-ZOHO-ID" {
		t.Errorf("zohoId = %q, want %q", zohoId, "NEW-ZOHO-ID")
	}
	if zoho.createOrderCalls != 1 || zoho.updateOrderCalls != 0 {
		t.Errorf("create=%d update=%d, want create=1 update=0", zoho.createOrderCalls, zoho.updateOrderCalls)
	}
	if repo.changeZohoIdCalls != 1 || repo.changedTo != "NEW-ZOHO-ID" {
		t.Errorf("zoho_id written back %d time(s) as %q, want 1 as NEW-ZOHO-ID", repo.changeZohoIdCalls, repo.changedTo)
	}
	if zoho.createPaymentCalls != 1 {
		t.Errorf("CreatePayment calls = %d, want 1", zoho.createPaymentCalls)
	}
}

// The regression this endpoint fix is for: re-pushing an order that is already in Zoho must
// UPDATE that record. Creating a second one would duplicate the order and orphan the record the
// reverse webhook, the payment link and zoho_modified_time all point at.
func TestPushOrderToZoho_UpdatesWhenAlreadySynced(t *testing.T) {
	repo := &fakeRepo{zohoId: "739178000059413569", order: pushableOrder()}
	zoho := &fakeZoho{}
	core := pushTestCore(repo, zoho)

	zohoId, err := core.PushOrderToZoho(16939)
	if err != nil {
		t.Fatalf("PushOrderToZoho() error = %v", err)
	}

	if zoho.createOrderCalls != 0 {
		t.Errorf("CreateOrder called %d time(s) — a re-push must not duplicate the order", zoho.createOrderCalls)
	}
	if zoho.updateOrderCalls != 1 {
		t.Fatalf("UpdateOrder calls = %d, want 1", zoho.updateOrderCalls)
	}
	if zoho.updatedID != "739178000059413569" {
		t.Errorf("updated Zoho id = %q, want the existing one", zoho.updatedID)
	}
	if zohoId != "739178000059413569" {
		t.Errorf("zohoId = %q, want the existing one (an update keeps its id)", zohoId)
	}
	// The id did not change, so nothing needs writing back.
	if repo.changeZohoIdCalls != 0 {
		t.Errorf("ChangeOrderZohoId called %d time(s) on an update, want 0", repo.changeZohoIdCalls)
	}
	// The payment is a separate linked record: re-pushing must not add a second one for the
	// same Stripe intent.
	if zoho.createPaymentCalls != 0 {
		t.Errorf("CreatePayment called %d time(s) on a re-push — that would duplicate the payment", zoho.createPaymentCalls)
	}
}

// "[B2B]" is a sentinel, not a Zoho record id — it must never be used as an update target.
func TestZohoOrderExists(t *testing.T) {
	cases := map[string]bool{
		"":                   false,
		b2bZohoId:            false,
		"739178000059413569": true,
	}
	for id, want := range cases {
		if got := zohoOrderExists(id); got != want {
			t.Errorf("zohoOrderExists(%q) = %v, want %v", id, got, want)
		}
	}
}
