package health

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"zohoclient/entity"
)

type stubCore struct {
	status entity.ServiceStatus
	calls  int
}

func (s *stubCore) Status() entity.ServiceStatus {
	s.calls++
	return s.status
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCheck_HealthyServiceAnswers200(t *testing.T) {
	core := &stubCore{status: entity.ServiceStatus{
		Status: entity.StatusOK,
		Uptime: "3d 4h",
		Components: []entity.ComponentStatus{
			{Name: "database", State: entity.ComponentUp},
			{Name: "mongo", State: entity.ComponentDisabled},
		},
	}}

	rec := httptest.NewRecorder()
	Check(discardLogger(), core)(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != entity.StatusOK || body["uptime"] != "3d 4h" {
		t.Errorf("body = %v, want the state and uptime", body)
	}
}

func TestCheck_DegradedServiceAnswers503(t *testing.T) {
	core := &stubCore{status: entity.ServiceStatus{
		Status:     entity.StatusDegraded,
		Uptime:     "10s",
		Components: []entity.ComponentStatus{{Name: "database", State: entity.ComponentDown, Detail: "refused"}},
	}}

	rec := httptest.NewRecorder()
	Check(discardLogger(), core)(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

// The probe is reachable without a token, so it must not disclose which shop this is, what it is
// syncing, or why it is unhappy.
func TestCheck_DisclosesNothingBeyondStateAndUptime(t *testing.T) {
	core := &stubCore{status: entity.ServiceStatus{
		Status: entity.StatusDegraded,
		Site:   "dark-ua",
		Env:    "production",
		Uptime: "10s",
		Components: []entity.ComponentStatus{
			{Name: "database", State: entity.ComponentDown, Detail: "Access denied for user 'zoho'"},
		},
		Orders: entity.OrderSyncStatus{TotalSynced: 128, LastError: "connection lost"},
	}}

	rec := httptest.NewRecorder()
	Check(discardLogger(), core)(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	body := rec.Body.String()
	for _, leak := range []string{"dark-ua", "production", "Access denied", "connection lost", "128"} {
		if strings.Contains(body, leak) {
			t.Errorf("public probe leaked %q in:\n%s", leak, body)
		}
	}

	var decoded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(decoded) != 2 {
		t.Errorf("body has %d fields (%v), want exactly status and uptime", len(decoded), decoded)
	}
}

func TestStatus_ReturnsTheFullReport(t *testing.T) {
	core := &stubCore{status: entity.ServiceStatus{
		Status:     entity.StatusDegraded,
		Site:       "dark-ua",
		Components: []entity.ComponentStatus{{Name: "database", State: entity.ComponentDown, Detail: "refused"}},
		Orders:     entity.OrderSyncStatus{TotalSynced: 128},
	}}

	rec := httptest.NewRecorder()
	Status(discardLogger(), core)(rec, httptest.NewRequest(http.MethodGet, "/zoho/status", nil))

	// The caller asked for the report; a degraded service is the answer, not a failed request.
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	for _, want := range []string{"dark-ua", "refused", "128"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("report is missing %q in:\n%s", want, rec.Body.String())
		}
	}
}
