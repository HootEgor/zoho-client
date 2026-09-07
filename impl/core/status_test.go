package core

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/config"
)

// The status probes embed their interfaces, so any method the report is not supposed to call is
// nil and panics loudly rather than silently succeeding.
type statusRepo struct {
	Repository
	pingErr error
}

func (s *statusRepo) Ping() error       { return s.pingErr }
func (s *statusRepo) PoolStats() string { return "open: 2, inuse: 0, idle: 2" }

type statusMongo struct {
	MongoRepository
	pingErr error
}

func (s *statusMongo) Ping() error { return s.pingErr }

type statusZoho struct {
	Zoho
	expiry time.Time
	valid  bool
}

func (s *statusZoho) TokenStatus() (time.Time, bool) { return s.expiry, s.valid }

func statusCore() *Core {
	return &Core{
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		site:      config.DefaultSiteSettings(),
		env:       "test",
		startedAt: time.Now().Add(-90 * time.Minute),
		repo:      &statusRepo{},
		mongoRepo: &statusMongo{},
		zoho:      &statusZoho{expiry: time.Now().Add(30 * time.Minute), valid: true},
	}
}

func componentByName(t *testing.T, s entity.ServiceStatus, name string) entity.ComponentStatus {
	t.Helper()
	for _, c := range s.Components {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q component in %+v", name, s.Components)
	return entity.ComponentStatus{}
}

func TestStatus_HealthyService(t *testing.T) {
	c := statusCore()
	c.recordOrderRun(time.Now().Add(-time.Second), 3, 3, 0, nil)

	s := c.Status()

	if s.Status != entity.StatusOK {
		t.Errorf("Status = %q, want %q (components: %+v)", s.Status, entity.StatusOK, s.Components)
	}
	if s.Env != "test" {
		t.Errorf("Env = %q, want test", s.Env)
	}
	if s.Uptime != "1h 30m" {
		t.Errorf("Uptime = %q, want 1h 30m", s.Uptime)
	}
	for _, name := range []string{"database", "mongo", "zoho", "order-sync"} {
		if got := componentByName(t, s, name); got.State != entity.ComponentUp {
			t.Errorf("component %s = %q (%s), want up", name, got.State, got.Detail)
		}
	}
	if got := componentByName(t, s, "database"); got.Detail != "open: 2, inuse: 0, idle: 2" {
		t.Errorf("database detail = %q, want the pool stats", got.Detail)
	}
}

func TestStatus_DatabaseDownMakesServiceDegraded(t *testing.T) {
	c := statusCore()
	c.repo = &statusRepo{pingErr: errors.New("dial tcp: connection refused")}

	s := c.Status()

	if s.Status != entity.StatusDegraded {
		t.Errorf("Status = %q, want %q", s.Status, entity.StatusDegraded)
	}
	db := componentByName(t, s, "database")
	if db.State != entity.ComponentDown || db.Detail != "dial tcp: connection refused" {
		t.Errorf("database component = %+v, want down with the ping error", db)
	}
}

// A subsystem switched off in the config is working as intended and must not read as a fault.
func TestStatus_DisabledMongoIsNotDegraded(t *testing.T) {
	c := statusCore()
	c.mongoRepo = nil

	s := c.Status()

	if s.Status != entity.StatusOK {
		t.Errorf("Status = %q, want %q", s.Status, entity.StatusOK)
	}
	if got := componentByName(t, s, "mongo"); got.State != entity.ComponentDisabled {
		t.Errorf("mongo component = %+v, want disabled", got)
	}
}

// A freshly started service holds no Zoho token and has run no poll yet. Neither is a fault, and
// neither may be reported as health the service cannot vouch for.
func TestStatus_FreshServiceIsUnknownNotDown(t *testing.T) {
	c := statusCore()
	c.zoho = &statusZoho{}

	s := c.Status()

	if s.Status != entity.StatusOK {
		t.Errorf("Status = %q, want %q", s.Status, entity.StatusOK)
	}
	if got := componentByName(t, s, "zoho"); got.State != entity.ComponentUnknown {
		t.Errorf("zoho component = %+v, want unknown", got)
	}
	if got := componentByName(t, s, "order-sync"); got.State != entity.ComponentUnknown {
		t.Errorf("order-sync component = %+v, want unknown", got)
	}
	if s.Orders.LastRunAt != nil {
		t.Errorf("LastRunAt = %v, want nil before the first poll", s.Orders.LastRunAt)
	}
}

func TestStatus_FailedPollMakesServiceDegraded(t *testing.T) {
	c := statusCore()
	c.recordOrderRun(time.Now(), 0, 0, 0, errors.New("get new orders: connection lost"))

	s := c.Status()

	if s.Status != entity.StatusDegraded {
		t.Errorf("Status = %q, want %q", s.Status, entity.StatusDegraded)
	}
	sync := componentByName(t, s, "order-sync")
	if sync.State != entity.ComponentDown || sync.Detail != "get new orders: connection lost" {
		t.Errorf("order-sync component = %+v, want down with the poll error", sync)
	}
}

// A pass that succeeds after a failure clears the down state, but keeps the error text — the
// reason the previous pass failed is usually what someone reading the report wants.
func TestStatus_RecoveredPollKeepsTheLastError(t *testing.T) {
	c := statusCore()
	c.recordOrderRun(time.Now().Add(-time.Minute), 0, 0, 0, errors.New("connection lost"))
	c.recordOrderRun(time.Now(), 2, 2, 0, nil)

	s := c.Status()

	if s.Status != entity.StatusOK {
		t.Errorf("Status = %q, want %q", s.Status, entity.StatusOK)
	}
	if got := componentByName(t, s, "order-sync"); got.State != entity.ComponentUp {
		t.Errorf("order-sync component = %+v, want up", got)
	}
	if s.Orders.LastError != "connection lost" {
		t.Errorf("LastError = %q, want it kept after recovery", s.Orders.LastError)
	}
}

func TestRecordOrderRun_AccumulatesTotals(t *testing.T) {
	c := statusCore()
	c.recordOrderRun(time.Now(), 5, 4, 1, nil)
	c.recordOrderRun(time.Now(), 3, 3, 0, nil)

	s := c.Status().Orders

	if s.TotalSynced != 7 || s.TotalFailed != 1 {
		t.Errorf("totals = %d synced / %d failed, want 7/1", s.TotalSynced, s.TotalFailed)
	}
	if s.LastQueued != 3 || s.LastSynced != 3 || s.LastFailed != 0 {
		t.Errorf("last pass = %d/%d/%d, want 3/3/0", s.LastQueued, s.LastSynced, s.LastFailed)
	}
}

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{0, "0s"},
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m 30s"},
		{time.Hour + 30*time.Minute, "1h 30m"},
		{25 * time.Hour, "1d 1h"},
		{75 * time.Hour, "3d 3h"},
	}
	for _, tt := range tests {
		if got := formatUptime(tt.in); got != tt.want {
			t.Errorf("formatUptime(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
