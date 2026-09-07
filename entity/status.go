package entity

import "time"

// Overall service states, as reported by ServiceStatus.Status.
const (
	// StatusOK means no component reported itself down.
	StatusOK = "ok"
	// StatusDegraded means at least one component is down. The process may still be serving.
	StatusDegraded = "degraded"
)

// Component states. Only ComponentDown makes the service degraded: a subsystem that is switched
// off by configuration is working as intended, and one that has not been exercised yet says so
// rather than claiming health it cannot vouch for.
const (
	ComponentUp       = "up"
	ComponentDown     = "down"
	ComponentDisabled = "disabled"
	ComponentUnknown  = "unknown"
)

// ServiceStatus is the snapshot both the /health endpoint and the Telegram /status command render.
type ServiceStatus struct {
	Status        string            `json:"status"`
	Site          string            `json:"site"`
	Env           string            `json:"env"`
	DryRun        bool              `json:"dry_run"`
	StartedAt     time.Time         `json:"started_at"`
	Uptime        string            `json:"uptime"`
	UptimeSeconds int64             `json:"uptime_seconds"`
	Features      FeatureStatus     `json:"features"`
	Components    []ComponentStatus `json:"components"`
	Orders        OrderSyncStatus   `json:"orders"`
}

// FeatureStatus reports which optional subsystems this shop runs. A feature that is off is inert,
// not idle — the service never touches the columns or APIs it owns.
type FeatureStatus struct {
	Payments     bool `json:"payments"`
	CustomerSync bool `json:"customer_sync"`
	B2B          bool `json:"b2b"`
	SmartSender  bool `json:"smartsender"`
}

// ComponentStatus is one dependency's state. Detail carries the reason for a down or unknown
// state, or a short fact worth reading for an up one.
type ComponentStatus struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// OrderSyncStatus describes the last completed pass of the order poller and the totals since the
// process started. LastRunAt is nil until the first pass finishes.
type OrderSyncStatus struct {
	LastRunAt    *time.Time `json:"last_run_at,omitempty"`
	LastRunMs    int64      `json:"last_run_ms"`
	LastQueued   int        `json:"last_queued"`
	LastSynced   int        `json:"last_synced"`
	LastFailed   int        `json:"last_failed"`
	TotalSynced  int64      `json:"total_synced"`
	TotalFailed  int64      `json:"total_failed"`
	LastError    string     `json:"last_error,omitempty"`
	LastErrorAt  *time.Time `json:"last_error_at,omitempty"`
	PollInterval string     `json:"poll_interval"`
}

// Degraded reports whether any component is down.
func (s ServiceStatus) Degraded() bool {
	for _, c := range s.Components {
		if c.State == ComponentDown {
			return true
		}
	}
	return false
}
