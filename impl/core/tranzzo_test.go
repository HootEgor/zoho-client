package core

import (
	"testing"
	"zohoclient/entity"
	"zohoclient/internal/config"
)

// tranzzoRepo records the queue tasks UpdateOrder enqueues.
type tranzzoRepo struct {
	*webhookRepo
	events   []entity.TranzzoOrderEvent
	orders   []int64
	failNext bool
}

func (r *tranzzoRepo) EnqueueTranzzoOrderEvent(orderId int64, event entity.TranzzoOrderEvent) error {
	if r.failNext {
		return errEnqueue
	}
	r.orders = append(r.orders, orderId)
	r.events = append(r.events, event)
	return nil
}

var errEnqueue = &enqueueError{}

type enqueueError struct{}

func (*enqueueError) Error() string { return "queue unavailable" }

// tranzzoCore is webhookTestCore on a shop whose payment source is tranzzo.
func tranzzoCore(repo *tranzzoRepo, dryRun bool) *Core {
	core := webhookTestCore(repo.webhookRepo, dryRun)
	core.repo = repo

	site := config.DefaultSiteSettings()
	site.PaymentSource = config.PaymentSourceTranzzo
	core.site = site

	return core
}

func tranzzoTestRepo() *tranzzoRepo {
	return &tranzzoRepo{webhookRepo: webhookTestRepo()}
}

// The status-only path carries every plain status change a manager makes, cancellations included,
// so it must reach the shop's module too — not only the path that rewrites the basket.
func TestUpdateOrder_StatusOnlyEnqueuesTranzzoTask(t *testing.T) {
	repo := tranzzoTestRepo()
	core := tranzzoCore(repo, false)

	update := &entity.ApiOrder{
		ZohoID:       "739178000059413569",
		Status:       "Перевірка та збір", // status id 5 in the default map
		GrandTotal:   468.00,
		ModifiedTime: "2026-09-07T12:00:00+02:00",
		OrderedItems: []entity.ApiOrderedItem{
			{ZohoID: "Z1", Price: 52.8455, Total: 422.764, Quantity: 8},
		},
	}

	if err := core.UpdateOrder(update); err != nil {
		t.Fatalf("UpdateOrder() error = %v", err)
	}

	if len(repo.events) != 1 {
		t.Fatalf("enqueued %d tasks, want 1", len(repo.events))
	}
	got := repo.events[0]
	if got.Status != "Перевірка та збір" {
		t.Errorf("Status = %q, want the picklist value verbatim", got.Status)
	}
	if got.Cancel {
		t.Error("Cancel set on an ordinary status change")
	}
	if got.ZohoID != update.ZohoID {
		t.Errorf("ZohoID = %q, want %q", got.ZohoID, update.ZohoID)
	}
	if repo.orders[0] != repo.webhookRepo.orderId {
		t.Errorf("enqueued for order %d, want %d", repo.orders[0], repo.webhookRepo.orderId)
	}
}

// A cancellation must carry cancel:true. The module honours the flag over its own status list, so
// the hold is released even when that list's wording has drifted from this shop's Zoho picklist.
func TestUpdateOrder_CancellationCarriesTheFlag(t *testing.T) {
	repo := tranzzoTestRepo()
	core := tranzzoCore(repo, false)

	// The default map names no canceled status, so give this site one that resolves.
	cfg := &config.Config{}
	cfg.Zoho.OrderStatusMap = map[int]string{1: "Нове", 7: "Відмінено"}
	site, err := cfg.SiteSettings()
	if err != nil {
		t.Fatalf("SiteSettings() = %v", err)
	}
	site.PaymentSource = config.PaymentSourceTranzzo
	core.site = site

	update := &entity.ApiOrder{
		ZohoID:       "739178000059413569",
		Status:       site.OrderStatusName(site.StatusCanceled),
		GrandTotal:   468.00,
		ModifiedTime: "2026-09-07T12:00:00+02:00",
		OrderedItems: []entity.ApiOrderedItem{
			{ZohoID: "Z1", Price: 52.8455, Total: 422.764, Quantity: 8},
		},
	}

	if err := core.UpdateOrder(update); err != nil {
		t.Fatalf("UpdateOrder() error = %v", err)
	}
	if len(repo.events) != 1 {
		t.Fatalf("enqueued %d tasks, want 1", len(repo.events))
	}
	if !repo.events[0].Cancel {
		t.Error("Cancel not set although the status resolved to the canceled id")
	}
}

// A status Zoho sent that this shop's map cannot resolve leaves the order where it was, and must
// not reach the module either: it matches the status as text against its own capture/cancel lists,
// so a phrase absent from zoho.order_status_map but present there would move money on a status
// this service could not place.
func TestUpdateOrder_UnresolvedStatusIsNotForwarded(t *testing.T) {
	repo := tranzzoTestRepo()
	core := tranzzoCore(repo, false)

	update := &entity.ApiOrder{
		ZohoID: "739178000059413569",
		// Not in the default map. A real one: the UA shop's Zoho carries this picklist value.
		Status:       "Рахунок виставлено",
		GrandTotal:   468.00,
		ModifiedTime: "2026-09-07T12:00:00+02:00",
		OrderedItems: []entity.ApiOrderedItem{
			{ZohoID: "Z1", Price: 52.8455, Total: 422.764, Quantity: 8},
		},
	}

	if err := core.UpdateOrder(update); err != nil {
		t.Fatalf("UpdateOrder() error = %v", err)
	}
	if len(repo.events) != 0 {
		t.Errorf("enqueued %d tasks for an unresolved status, want 0", len(repo.events))
	}
}

// Dry-run must not queue: the module acts on these rows with real money.
func TestUpdateOrder_DryRunWritesNoTranzzoTask(t *testing.T) {
	repo := tranzzoTestRepo()
	core := tranzzoCore(repo, true)

	update := &entity.ApiOrder{
		ZohoID:       "739178000059413569",
		Status:       "Перевірка та збір",
		GrandTotal:   468.00,
		ModifiedTime: "2026-09-07T12:00:00+02:00",
		OrderedItems: []entity.ApiOrderedItem{
			{ZohoID: "Z1", Price: 52.8455, Total: 422.764, Quantity: 8},
		},
	}

	if err := core.UpdateOrder(update); err != nil {
		t.Fatalf("UpdateOrder() error = %v", err)
	}
	if len(repo.events) != 0 {
		t.Errorf("dry run enqueued %d tasks, want 0", len(repo.events))
	}
}

// A shop on the wfsync source has no oc_tranzzo_queue at all, so nothing may be written to it.
func TestUpdateOrder_WfsyncShopEnqueuesNothing(t *testing.T) {
	repo := tranzzoTestRepo()
	core := tranzzoCore(repo, false)
	core.site = config.DefaultSiteSettings() // payment source: wfsync

	update := &entity.ApiOrder{
		ZohoID:       "739178000059413569",
		Status:       "Перевірка та збір",
		GrandTotal:   468.00,
		ModifiedTime: "2026-09-07T12:00:00+02:00",
		OrderedItems: []entity.ApiOrderedItem{
			{ZohoID: "Z1", Price: 52.8455, Total: 422.764, Quantity: 8},
		},
	}

	if err := core.UpdateOrder(update); err != nil {
		t.Fatalf("UpdateOrder() error = %v", err)
	}
	if len(repo.events) != 0 {
		t.Errorf("wfsync shop enqueued %d tasks, want 0", len(repo.events))
	}
}

// A queue that will not take the row must not fail the update: the order change is already
// committed, and a 500 would have Zoho resend a webhook that already landed.
func TestUpdateOrder_EnqueueFailureDoesNotFailTheUpdate(t *testing.T) {
	repo := tranzzoTestRepo()
	repo.failNext = true
	core := tranzzoCore(repo, false)

	update := &entity.ApiOrder{
		ZohoID:       "739178000059413569",
		Status:       "Перевірка та збір",
		GrandTotal:   468.00,
		ModifiedTime: "2026-09-07T12:00:00+02:00",
		OrderedItems: []entity.ApiOrderedItem{
			{ZohoID: "Z1", Price: 52.8455, Total: 422.764, Quantity: 8},
		},
	}

	if err := core.UpdateOrder(update); err != nil {
		t.Fatalf("UpdateOrder() error = %v, want nil despite the queue being down", err)
	}
}

// The module's parseTask rejects a payload with neither a status nor a flag, so we must not spend
// a queue row on one.
func TestTranzzoOrderEvent_ValidityMirrorsTheModule(t *testing.T) {
	tests := []struct {
		name  string
		event entity.TranzzoOrderEvent
		want  bool
	}{
		{"status alone", entity.TranzzoOrderEvent{Status: "Нове"}, true},
		{"cancel flag alone", entity.TranzzoOrderEvent{Cancel: true}, true},
		{"takeover flag alone", entity.TranzzoOrderEvent{ZohoManaged: true}, true},
		{"sum and id but nothing to act on", entity.TranzzoOrderEvent{Sum: 300, ZohoID: "1"}, false},
		{"empty", entity.TranzzoOrderEvent{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

// paymentTakeoverRepo answers the reads UpdatePayments performs and records queue tasks.
type paymentTakeoverRepo struct {
	*tranzzoRepo
	zohoPaymentId string
	state         *paymentState
}

func (r *paymentTakeoverRepo) GetOrderZohoPaymentId(int64) (string, error) {
	return r.zohoPaymentId, nil
}

// paymentState is the last wf_payment_* write, or nil if none was attempted.
type paymentState struct {
	status      string
	paymentId   string
	amountMinor int64
}

func (r *paymentTakeoverRepo) SetOrderPaymentState(_ int64, status, paymentId string, amountMinor int64) error {
	r.state = &paymentState{status: status, paymentId: paymentId, amountMinor: amountMinor}
	return nil
}

func takeoverCore(ourPaymentId string, tranzzo bool) (*Core, *paymentTakeoverRepo) {
	inner := tranzzoTestRepo()
	repo := &paymentTakeoverRepo{tranzzoRepo: inner, zohoPaymentId: ourPaymentId}

	core := tranzzoCore(inner, false)
	core.repo = repo
	if !tranzzo {
		core.site = config.DefaultSiteSettings()
	}
	return core, repo
}

// A payment the service did not create was raised inside Zoho: payment control passes over, and
// the shop's module must be told to stand down.
func TestUpdatePayments_ForeignPaymentHandsControlToZoho(t *testing.T) {
	core, repo := takeoverCore("PAY-OURS", true)

	err := core.UpdatePayments(&entity.ApiPaymentUpdate{
		ZohoID: "739178000059413569",
		Payments: []entity.ApiPayment{
			{ZohoID: "PAY-OURS", Name: zohoPaymentName(repo.webhookRepo.orderId), Status: "Оплачено"},
			{ZohoID: "PAY-FROM-ZOHO", Name: "Доплата", Status: "Створено"},
		},
	})
	if err != nil {
		t.Fatalf("UpdatePayments() error = %v", err)
	}

	if len(repo.tranzzoRepo.events) != 1 {
		t.Fatalf("enqueued %d tasks, want 1", len(repo.tranzzoRepo.events))
	}
	got := repo.tranzzoRepo.events[0]
	if !got.ZohoManaged {
		t.Error("ZohoManaged not set on the takeover event")
	}
	if got.Status != "" || got.Cancel {
		t.Errorf("takeover carried a status (%q) or cancel (%v); it should carry the flag alone",
			got.Status, got.Cancel)
	}
	if !got.Valid() {
		t.Error("the module would reject this payload as no_status")
	}
}

// Only our own payment in the list: nothing changed hands, so nothing is queued. Takeover is a
// one-way latch on the module's side, so a false positive here is not recoverable.
func TestUpdatePayments_OwnPaymentDoesNotHandOverControl(t *testing.T) {
	core, repo := takeoverCore("PAY-OURS", true)

	err := core.UpdatePayments(&entity.ApiPaymentUpdate{
		ZohoID: "739178000059413569",
		Payments: []entity.ApiPayment{
			{ZohoID: "PAY-OURS", Name: zohoPaymentName(repo.webhookRepo.orderId), Status: "Оплачено"},
		},
	})
	if err != nil {
		t.Fatalf("UpdatePayments() error = %v", err)
	}
	if len(repo.tranzzoRepo.events) != 0 {
		t.Errorf("enqueued %d tasks for our own payment, want 0", len(repo.tranzzoRepo.events))
	}
}

// The race the Name check exists for: CreatePayment has returned an id but UpdateOrderZohoPayment
// has not stored it yet, so zoho_payment_id is still empty when the webhook arrives. Our own
// record must not be read as someone else's.
func TestUpdatePayments_UnrecordedOwnPaymentIsNotATakeover(t *testing.T) {
	core, repo := takeoverCore("", true)

	err := core.UpdatePayments(&entity.ApiPaymentUpdate{
		ZohoID: "739178000059413569",
		Payments: []entity.ApiPayment{
			{ZohoID: "PAY-OURS", Name: zohoPaymentName(repo.webhookRepo.orderId), Status: "Створено"},
		},
	})
	if err != nil {
		t.Fatalf("UpdatePayments() error = %v", err)
	}
	if len(repo.tranzzoRepo.events) != 0 {
		t.Errorf("enqueued %d tasks while our own id was not yet recorded, want 0",
			len(repo.tranzzoRepo.events))
	}
}

// A shop with no payment record of ours at all, and a payment sitting in Zoho: that payment was
// raised there.
func TestUpdatePayments_PaymentWithNoRecordOfOursIsATakeover(t *testing.T) {
	core, repo := takeoverCore("", true)

	err := core.UpdatePayments(&entity.ApiPaymentUpdate{
		ZohoID: "739178000059413569",
		Payments: []entity.ApiPayment{
			{ZohoID: "PAY-FROM-ZOHO", Name: "Оплата за замовлення", Status: "Створено"},
		},
	})
	if err != nil {
		t.Fatalf("UpdatePayments() error = %v", err)
	}
	if len(repo.tranzzoRepo.events) != 1 {
		t.Fatalf("enqueued %d tasks, want 1", len(repo.tranzzoRepo.events))
	}
	if !repo.tranzzoRepo.events[0].ZohoManaged {
		t.Error("ZohoManaged not set")
	}
}

// Shop 1 runs wfsync and has no oc_tranzzo_queue: a foreign payment there is worth logging and
// nothing else.
func TestUpdatePayments_WfsyncShopNeverHandsOverControl(t *testing.T) {
	core, repo := takeoverCore("PAY-OURS", false)

	err := core.UpdatePayments(&entity.ApiPaymentUpdate{
		ZohoID: "739178000059413569",
		Payments: []entity.ApiPayment{
			{ZohoID: "PAY-FROM-ZOHO", Name: "Доплата", Status: "Створено"},
		},
	})
	if err != nil {
		t.Fatalf("UpdatePayments() error = %v", err)
	}
	if len(repo.tranzzoRepo.events) != 0 {
		t.Errorf("wfsync shop enqueued %d tasks, want 0", len(repo.tranzzoRepo.events))
	}
}

// The two-webhook sequence a correction produces. Zoho keeps one active payment per Sales Order:
// raising a new one cancels the previous, so the shop must end up looking at the NEW payment, not
// the cancelled one that still sits in the list.
func TestUpdatePayments_WriteBackFollowsTheLivePayment(t *testing.T) {
	tests := []struct {
		name     string
		payments []entity.ApiPayment
		want     paymentState
	}{
		{
			// First webhook: the manager cancelled ours. Nothing is live, so the settled record
			// is the answer — the order's payment really is off.
			name: "cancellation alone",
			payments: []entity.ApiPayment{
				{ZohoID: "PAY-ZOHO-1", Name: "Доплата", Status: "Скасовано", Sum: 300},
			},
			want: paymentState{status: "void", paymentId: "PAY-ZOHO-1", amountMinor: 30000},
		},
		{
			// Second webhook: the new payment alongside the cancelled one.
			name: "cancelled then newly raised",
			payments: []entity.ApiPayment{
				{ZohoID: "PAY-ZOHO-1", Name: "Доплата", Status: "Скасовано", Sum: 300},
				{ZohoID: "PAY-ZOHO-2", Name: "Доплата 2", Status: "Створено", Sum: 450.55},
			},
			want: paymentState{status: "init", paymentId: "PAY-ZOHO-2", amountMinor: 45055},
		},
		{
			// The customer pays it.
			name: "captured",
			payments: []entity.ApiPayment{
				{ZohoID: "PAY-ZOHO-1", Name: "Доплата", Status: "Скасовано", Sum: 300},
				{ZohoID: "PAY-ZOHO-2", Name: "Доплата 2", Status: "Оплачено", Sum: 450.55},
			},
			want: paymentState{status: "capture", paymentId: "PAY-ZOHO-2", amountMinor: 45055},
		},
		{
			// A hold, which is the state the module calls auth.
			name: "funds held",
			payments: []entity.ApiPayment{
				{ZohoID: "PAY-ZOHO-2", Name: "Доплата 2", Status: "Кошти зарезервовано", Sum: 450.55},
			},
			want: paymentState{status: "auth", paymentId: "PAY-ZOHO-2", amountMinor: 45055},
		},
		{
			// Refunded and canceled both mean "money returned"; the module has one word for it.
			name: "refunded",
			payments: []entity.ApiPayment{
				{ZohoID: "PAY-ZOHO-2", Name: "Доплата 2", Status: "Відшкодовано", Sum: 450.55},
			},
			want: paymentState{status: "void", paymentId: "PAY-ZOHO-2", amountMinor: 45055},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, repo := takeoverCore("PAY-OURS", true)

			err := core.UpdatePayments(&entity.ApiPaymentUpdate{
				ZohoID:   "739178000059413569",
				Payments: tt.payments,
			})
			if err != nil {
				t.Fatalf("UpdatePayments() error = %v", err)
			}

			if repo.state == nil {
				t.Fatal("wf_payment_* was not written")
			}
			if *repo.state != tt.want {
				t.Errorf("state = %+v, want %+v", *repo.state, tt.want)
			}
			// The module only looks at the order while processing a queue task, so the write is
			// pointless without one.
			if len(repo.tranzzoRepo.events) != 1 {
				t.Errorf("enqueued %d tasks, want 1 so the module reads the state back",
					len(repo.tranzzoRepo.events))
			}
		})
	}
}

// A picklist value this shop does not map says nothing actionable. Blanking wf_payment_status
// would read as "no payment" to both sides, which is worse than a stale one.
func TestUpdatePayments_UnmappedStatusLeavesTheColumnsAlone(t *testing.T) {
	core, repo := takeoverCore("PAY-OURS", true)

	err := core.UpdatePayments(&entity.ApiPaymentUpdate{
		ZohoID: "739178000059413569",
		Payments: []entity.ApiPayment{
			{ZohoID: "PAY-ZOHO-1", Name: "Доплата", Status: "Очікує підтвердження", Sum: 300},
		},
	})
	if err != nil {
		t.Fatalf("UpdatePayments() error = %v", err)
	}
	if repo.state != nil {
		t.Errorf("wrote %+v for an unmapped status, want no write", *repo.state)
	}
	// Control still changed hands — that part does not depend on reading the status.
	if len(repo.tranzzoRepo.events) != 1 {
		t.Errorf("enqueued %d takeover tasks, want 1", len(repo.tranzzoRepo.events))
	}
}

// Before a takeover the shop's own module owns these columns. Two writers on one column is the
// thing the takeover latch exists to prevent.
func TestUpdatePayments_NoTakeoverMeansNoWriteBack(t *testing.T) {
	core, repo := takeoverCore("PAY-OURS", true)

	err := core.UpdatePayments(&entity.ApiPaymentUpdate{
		ZohoID: "739178000059413569",
		Payments: []entity.ApiPayment{
			{ZohoID: "PAY-OURS", Name: zohoPaymentName(repo.webhookRepo.orderId), Status: "Оплачено", Sum: 468},
		},
	})
	if err != nil {
		t.Fatalf("UpdatePayments() error = %v", err)
	}
	if repo.state != nil {
		t.Errorf("wrote %+v while the shop's module still owns the columns", *repo.state)
	}
}

// Dry-run must not touch the shop.
func TestUpdatePayments_DryRunWritesNoPaymentState(t *testing.T) {
	core, repo := takeoverCore("PAY-OURS", true)
	core.dryRun = true

	err := core.UpdatePayments(&entity.ApiPaymentUpdate{
		ZohoID: "739178000059413569",
		Payments: []entity.ApiPayment{
			{ZohoID: "PAY-ZOHO-1", Name: "Доплата", Status: "Оплачено", Sum: 300},
		},
	})
	if err != nil {
		t.Fatalf("UpdatePayments() error = %v", err)
	}
	if repo.state != nil {
		t.Errorf("dry run wrote %+v", *repo.state)
	}
	if len(repo.tranzzoRepo.events) != 0 {
		t.Errorf("dry run enqueued %d tasks", len(repo.tranzzoRepo.events))
	}
}
