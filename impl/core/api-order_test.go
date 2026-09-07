package core

import (
	"fmt"
	"testing"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/database/sql"
)

// TestTotalsDiverged pins the tolerance against the case that motivated it: order 17134, where
// Zoho's subform was repriced from the 8.40% discount we sent to a flat 10% while every product
// and quantity stayed put. The items looked unchanged, so the totals were left alone and the two
// systems drifted 89 PLN apart in silence.
func TestTotalsDiverged(t *testing.T) {
	tests := []struct {
		name     string
		ocTotal  float64
		zohoTot  float64
		wantDiff float64
		want     bool
	}{
		{
			// The real divergence: Zoho reapplied the 10% at 19% VAT on the discounted base.
			name:     "order 17134 repriced in zoho",
			ocTotal:  5124.11,
			zohoTot:  5035.09,
			wantDiff: -89.02,
			want:     true,
		},
		{
			// The same order the day it synced: Zoho's own per-line rounding over 133 units.
			name:    "rounding drift on a long subform",
			ocTotal: 5124.11,
			zohoTot: 5123.93,
			want:    false,
		},
		{
			name:    "identical totals",
			ocTotal: 5124.11,
			zohoTot: 5124.11,
			want:    false,
		},
		{
			// Small order: the floor, not the rate, decides. 0.1% of 80 is 8 groszy.
			name:    "small order below the floor",
			ocTotal: 80.00,
			zohoTot: 80.90,
			want:    false,
		},
		{
			name:     "small order above the floor",
			ocTotal:  80.00,
			zohoTot:  78.50,
			wantDiff: -1.50,
			want:     true,
		},
		{
			// Zoho gaining money is as much a divergence as Zoho losing it.
			name:     "zoho higher than opencart",
			ocTotal:  5124.11,
			zohoTot:  5300.00,
			wantDiff: 175.89,
			want:     true,
		},
		{
			name:    "payload carries no grand total",
			ocTotal: 5124.11,
			zohoTot: 0,
			want:    false,
		},
		{
			name:    "opencart total missing",
			ocTotal: 0,
			zohoTot: 5035.09,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff, got := totalsDiverged(tt.ocTotal, tt.zohoTot)
			if got != tt.want {
				t.Errorf("totalsDiverged(%.2f, %.2f) = %v, want %v (diff %.2f)",
					tt.ocTotal, tt.zohoTot, got, tt.want, diff)
			}
			if tt.want && !approx(diff, tt.wantDiff, 0.005) {
				t.Errorf("diff = %.2f, want %.2f", diff, tt.wantDiff)
			}
		})
	}
}

// webhookRepo is the reverse-sync counterpart of fakeRepo: it answers the reads UpdateOrder
// performs and counts every write it attempts, so a dry-run test can assert the count is zero.
type webhookRepo struct {
	Repository
	orderId   int64
	order     *entity.CheckoutParams
	items     []sql.OrderProductSummary
	searchErr error

	searchCalls int

	statusCalls   int
	txCalls       int
	modifiedCalls int
}

func (r *webhookRepo) OrderSearchByZohoId(string) (int64, *entity.CheckoutParams, error) {
	r.searchCalls++
	if r.searchErr != nil {
		return 0, nil, r.searchErr
	}
	return r.orderId, r.order, nil
}

func (r *webhookRepo) GetOrderZohoModifiedTime(int64) (time.Time, error) {
	return time.Time{}, nil
}

func (r *webhookRepo) GetOrderProductsSummary(int64) ([]sql.OrderProductSummary, error) {
	return r.items, nil
}

func (r *webhookRepo) ChangeOrderStatus(int64, int64, string) error {
	r.statusCalls++
	return nil
}

func (r *webhookRepo) UpdateOrderWithTransaction(sql.OrderUpdateTransaction) error {
	r.txCalls++
	return nil
}

func (r *webhookRepo) SetOrderZohoModifiedTime(int64, time.Time) error {
	r.modifiedCalls++
	return nil
}

func webhookTestRepo() *webhookRepo {
	order := pushableOrder()
	order.CurrencyValue = 1.0
	return &webhookRepo{
		orderId: order.OrderId,
		order:   order,
		items: []sql.OrderProductSummary{
			{ZohoID: "Z1", Name: "P", Quantity: 8, TotalInCents: 42276},
		},
	}
}

func webhookTestCore(repo *webhookRepo, dryRun bool) *Core {
	core := pushTestCore(&fakeRepo{}, &fakeZoho{})
	core.repo = repo
	core.dryRun = dryRun
	return core
}

// A webhook whose subform still matches OpenCart only moves the status. Under dry-run even that
// single write must not happen.
func TestUpdateOrder_DryRunStatusOnly(t *testing.T) {
	repo := webhookTestRepo()
	core := webhookTestCore(repo, true)

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
	if repo.statusCalls != 0 {
		t.Errorf("ChangeOrderStatus called %d time(s) in dry run, want 0", repo.statusCalls)
	}
	if repo.modifiedCalls != 0 {
		t.Errorf("SetOrderZohoModifiedTime called %d time(s) in dry run, want 0 — storing it "+
			"would suppress the same webhook once dry-run is switched off", repo.modifiedCalls)
	}
	if repo.txCalls != 0 {
		t.Errorf("UpdateOrderWithTransaction called %d time(s) in dry run, want 0", repo.txCalls)
	}
}

// A webhook that changes the subform goes through the transaction path. Dry-run computes the
// reverse totals and reports them, but writes nothing.
func TestUpdateOrder_DryRunItemsChanged(t *testing.T) {
	repo := webhookTestRepo()
	core := webhookTestCore(repo, true)

	update := &entity.ApiOrder{
		ZohoID:     "739178000059413569",
		Status:     "Перевірка та збір",
		GrandTotal: 520.00,
		OrderedItems: []entity.ApiOrderedItem{
			{ZohoID: "Z1", Price: 52.8455, Total: 475.61, Quantity: 9},
		},
	}

	if err := core.UpdateOrder(update); err != nil {
		t.Fatalf("UpdateOrder() error = %v", err)
	}
	if repo.txCalls != 0 {
		t.Errorf("UpdateOrderWithTransaction called %d time(s) in dry run, want 0", repo.txCalls)
	}
	if repo.statusCalls != 0 {
		t.Errorf("ChangeOrderStatus called %d time(s) in dry run, want 0", repo.statusCalls)
	}
	if repo.modifiedCalls != 0 {
		t.Errorf("SetOrderZohoModifiedTime called %d time(s) in dry run, want 0", repo.modifiedCalls)
	}
}

// The control: with dry-run off the same webhook is applied as before.
func TestUpdateOrder_AppliesWhenNotDryRun(t *testing.T) {
	repo := webhookTestRepo()
	core := webhookTestCore(repo, false)

	update := &entity.ApiOrder{
		ZohoID:     "739178000059413569",
		Status:     "Перевірка та збір",
		GrandTotal: 520.00,
		OrderedItems: []entity.ApiOrderedItem{
			{ZohoID: "Z1", Price: 52.8455, Total: 475.61, Quantity: 9},
		},
	}

	if err := core.UpdateOrder(update); err != nil {
		t.Fatalf("UpdateOrder() error = %v", err)
	}
	if repo.txCalls != 1 {
		t.Errorf("UpdateOrderWithTransaction calls = %d, want 1", repo.txCalls)
	}
}

// A dry-run instance never writes a zoho_id, so every webhook Zoho sends for an order it thinks we
// synced finds nothing. That is the mode working, not a fault: warn and answer the caller normally
// instead of raising a DATABASE_ERROR and a 500 for each one.
func TestUpdateOrder_DryRunOrderNotFound(t *testing.T) {
	repo := webhookTestRepo()
	repo.searchErr = fmt.Errorf("order with zoho_id '739178000064567111': %w", sql.ErrOrderNotFound)
	core := webhookTestCore(repo, true)

	err := core.UpdateOrder(&entity.ApiOrder{ZohoID: "739178000064567111", Status: "Відправлено"})
	if err != nil {
		t.Errorf("UpdateOrder() error = %v, want nil so the webhook is not reported as a failure", err)
	}
	// The race the retries exist for needs a zoho_id write, which dry-run never performs.
	if repo.searchCalls != 1 {
		t.Errorf("OrderSearchByZohoId called %d time(s), want 1 — retrying cannot help in dry run",
			repo.searchCalls)
	}
}

// The leniency is scoped to "no such order". A database that cannot answer is still an error, in
// dry run as anywhere else — otherwise an outage would look like a quiet run.
func TestUpdateOrder_DryRunDatabaseErrorStillFails(t *testing.T) {
	repo := webhookTestRepo()
	repo.searchErr = fmt.Errorf("dial tcp 127.0.0.1:3306: connect: connection refused")
	core := webhookTestCore(repo, true)

	if err := core.UpdateOrder(&entity.ApiOrder{ZohoID: "739178000064567111"}); err == nil {
		t.Error("UpdateOrder() error = nil, want the database failure reported")
	}
}

// Outside dry run a missing order is still an error the caller can retry against.
func TestUpdateOrder_NotFoundStillFailsWhenLive(t *testing.T) {
	repo := webhookTestRepo()
	repo.searchErr = fmt.Errorf("order with zoho_id '739178000064567111': %w", sql.ErrOrderNotFound)
	core := webhookTestCore(repo, false)

	if err := core.UpdateOrder(&entity.ApiOrder{ZohoID: "739178000064567111"}); err == nil {
		t.Error("UpdateOrder() error = nil, want the missing order reported")
	}
	if repo.searchCalls != 5 {
		t.Errorf("OrderSearchByZohoId called %d time(s), want 5 — the write race is real when "+
			"zoho_id is being recorded", repo.searchCalls)
	}
}
