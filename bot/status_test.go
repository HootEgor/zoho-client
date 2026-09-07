package bot

import (
	"strings"
	"testing"
	"time"
	"zohoclient/entity"
)

func healthyStatus() entity.ServiceStatus {
	lastRun := time.Date(2026, 9, 7, 12, 3, 44, 0, time.Local)
	return entity.ServiceStatus{
		Status:    entity.StatusOK,
		Site:      "dark",
		Env:       "production",
		Uptime:    "3d 4h",
		StartedAt: lastRun.Add(-76 * time.Hour),
		Components: []entity.ComponentStatus{
			{Name: "database", State: entity.ComponentUp, Detail: "open: 2, inuse: 0, idle: 2"},
			{Name: "mongo", State: entity.ComponentUp},
			{Name: "zoho", State: entity.ComponentUp, Detail: "token valid for 42m 10s"},
			{Name: "order-sync", State: entity.ComponentUp, Detail: "last poll 41s ago"},
		},
		Features: entity.FeatureStatus{Payments: true, CustomerSync: true},
		Orders: entity.OrderSyncStatus{
			LastRunAt:    &lastRun,
			LastRunMs:    120,
			LastQueued:   3,
			LastSynced:   3,
			TotalSynced:  128,
			TotalFailed:  2,
			PollInterval: "2m0s",
		},
	}
}

func TestFormatStatus_HealthyService(t *testing.T) {
	msg := formatStatus(healthyStatus())

	for _, want := range []string{
		"✅ *Service: ok*",
		"Site: dark",
		"Env: production",
		"Uptime: 3d 4h",
		"database: up — open: 2, inuse: 0, idle: 2",
		"zoho: up — token valid for 42m 10s",
		"Poll interval: 2m0s",
		"Last poll: 2026\\-09\\-07 12:03:44 \\(120 ms\\)",
		"Queued 3 · synced 3 · failed 0",
		"Since start: 128 synced, 2 failed",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q\ngot:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "DRY RUN") {
		t.Errorf("dry run should not be announced when it is off\ngot:\n%s", msg)
	}
	if strings.Contains(msg, "Last error") {
		t.Errorf("no error section belongs on a clean run\ngot:\n%s", msg)
	}
}

func TestFormatStatus_DegradedServiceShowsTheReason(t *testing.T) {
	s := healthyStatus()
	s.Status = entity.StatusDegraded
	s.DryRun = true
	s.Components[0] = entity.ComponentStatus{
		Name: "database", State: entity.ComponentDown, Detail: "dial tcp: connection refused",
	}
	errAt := time.Date(2026, 9, 7, 12, 1, 0, 0, time.Local)
	s.Orders.LastError = "get new orders: connection lost"
	s.Orders.LastErrorAt = &errAt

	msg := formatStatus(s)

	for _, want := range []string{
		"❌ *Service: degraded*",
		"❌ database: down — dial tcp: connection refused",
		"DRY RUN",
		"*Last error*",
		"get new orders: connection lost",
		"2026\\-09\\-07 12:01:00",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q\ngot:\n%s", want, msg)
		}
	}
}

func TestFormatStatus_BeforeTheFirstPoll(t *testing.T) {
	s := healthyStatus()
	s.Orders = entity.OrderSyncStatus{PollInterval: "2m0s"}
	s.Components = []entity.ComponentStatus{
		{Name: "mongo", State: entity.ComponentDisabled, Detail: "order version history is off"},
		{Name: "zoho", State: entity.ComponentUnknown, Detail: "no valid access token cached"},
	}

	msg := formatStatus(s)

	if !strings.Contains(msg, "Last poll: none yet") {
		t.Errorf("expected the no-poll-yet wording\ngot:\n%s", msg)
	}
	if strings.Contains(msg, "Queued") {
		t.Errorf("per-pass counts are meaningless before the first poll\ngot:\n%s", msg)
	}
	if !strings.Contains(msg, "➖ mongo: disabled") {
		t.Errorf("expected the disabled icon\ngot:\n%s", msg)
	}
	if !strings.Contains(msg, "❔ zoho: unknown") {
		t.Errorf("expected the unknown icon\ngot:\n%s", msg)
	}
}

// The status message carries free-form detail from ping errors and Zoho, so its escaping is the
// part most likely to break Telegram's MarkdownV2 parser.
func TestFormatStatus_IsEscaped(t *testing.T) {
	s := healthyStatus()
	s.DryRun = true
	s.Site = "dark-ua"
	s.Components[0].Detail = "dial tcp 10.0.0.1:3306: connect: connection refused (retry=3)"
	errAt := time.Now()
	s.Orders.LastError = "get new orders: Error 1045 (28000): Access denied [user='zoho']"
	s.Orders.LastErrorAt = &errAt

	if bad := unescapedReserved(formatStatus(s), "*"); len(bad) > 0 {
		t.Errorf("unescaped MarkdownV2 characters %v in:\n%s", bad, formatStatus(s))
	}
}
