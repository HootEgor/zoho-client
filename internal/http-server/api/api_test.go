package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"zohoclient/entity"
	"zohoclient/internal/config"
)

// routerHandler embeds the Handler interface, so any method a routing test is not supposed to
// reach is nil and panics loudly rather than silently succeeding.
type routerHandler struct {
	Handler
	token   string
	updated []entity.ApiOrder
}

func (h *routerHandler) UpdateOrder(order *entity.ApiOrder) error {
	h.updated = append(h.updated, *order)
	return nil
}

func (h *routerHandler) AuthenticateByToken(token string) (*entity.UserAuth, error) {
	if token != h.token {
		return nil, http.ErrNoCookie
	}
	return &entity.UserAuth{}, nil
}

func (h *routerHandler) Status() entity.ServiceStatus {
	return entity.ServiceStatus{Status: entity.StatusOK, Site: "dark", Uptime: "1h 0m"}
}

func testServer(t *testing.T, basePath string) *Server {
	t.Helper()

	conf := &config.Config{}
	conf.Listen.BasePath = basePath
	conf.Listen.ApiKey = ""

	server, err := New(conf, config.DefaultSiteSettings(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), &routerHandler{token: "secret"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return server
}

func do(t *testing.T, server *Server, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

// The probe has to answer a load balancer that holds no token.
func TestRouter_HealthNeedsNoToken(t *testing.T) {
	server := testServer(t, "")

	rec := do(t, server, http.MethodGet, "/zoho/health", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d\nbody: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// Nothing may sit at the domain root: two instances published on one domain would collide there,
// which is the whole reason every route is mounted under a namespace.
func TestRouter_ServesNothingAtTheDomainRoot(t *testing.T) {
	server := testServer(t, "")

	for _, path := range []string{"/", "/health", "/status"} {
		if rec := do(t, server, http.MethodGet, path, "secret"); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}
}

// The point of the config key: a second instance owns a different namespace, so one reverse proxy
// can route both by path without rewriting.
func TestRouter_BasePathIsConfigurable(t *testing.T) {
	server := testServer(t, "zoho-ua")

	if rec := do(t, server, http.MethodGet, "/zoho-ua/health", ""); rec.Code != http.StatusOK {
		t.Errorf("GET /zoho-ua/health: status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec := do(t, server, http.MethodGet, "/zoho-ua/status", "secret"); rec.Code != http.StatusOK {
		t.Errorf("GET /zoho-ua/status: status = %d, want %d", rec.Code, http.StatusOK)
	}
	// The default namespace belongs to the other instance now.
	if rec := do(t, server, http.MethodGet, "/zoho/health", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET /zoho/health: status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := server.BasePath(); got != "/zoho-ua" {
		t.Errorf("BasePath() = %q, want /zoho-ua", got)
	}
}

// Everything the probe does not disclose stays behind the token — including the detailed report.
func TestRouter_StatusRequiresToken(t *testing.T) {
	server := testServer(t, "")

	if rec := do(t, server, http.MethodGet, "/zoho/status", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a token: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if rec := do(t, server, http.MethodGet, "/zoho/status", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Errorf("with a wrong token: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	rec := do(t, server, http.MethodGet, "/zoho/status", "secret")
	if rec.Code != http.StatusOK {
		t.Errorf("with the token: status = %d, want %d\nbody: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// Moving the auth middleware into a group must not have unauthenticated any existing route.
func TestRouter_ExistingRoutesStillRequireToken(t *testing.T) {
	server := testServer(t, "")

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/zoho/webhook/order"},
		{http.MethodPost, "/zoho/webhook/b2b"},
		{http.MethodGet, "/zoho/push/order/16939"},
	} {
		rec := do(t, server, route.method, route.path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want %d", route.method, route.path, rec.Code, http.StatusUnauthorized)
		}
	}
}

func TestNormalizeBasePath(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  string
		valid bool
	}{
		{"unset falls back to the default", "", "/zoho", true},
		{"blank falls back to the default", "   ", "/zoho", true},
		{"bare segment", "zoho", "/zoho", true},
		{"leading slash", "/zoho", "/zoho", true},
		{"trailing slash", "zoho/", "/zoho", true},
		{"both slashes", "/zoho-ua/", "/zoho-ua", true},
		{"nested", "shop/zoho", "/shop/zoho", true},
		{"a lone slash is the root, which is the default instead", "/", "/zoho", true},
		{"doubled slash", "shop//zoho", "", false},
		{"chi parameter syntax", "{shop}", "", false},
		{"space inside", "zoho ua", "", false},
		{"query separator", "zoho?x", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeBasePath(tt.in)
			if tt.valid && err != nil {
				t.Fatalf("NormalizeBasePath(%q) error = %v, want %q", tt.in, err, tt.want)
			}
			if !tt.valid {
				if err == nil {
					t.Fatalf("NormalizeBasePath(%q) = %q, want an error", tt.in, got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("NormalizeBasePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// A base path chi cannot mount must stop the process at startup, not panic on the first request.
func TestNew_RejectsAnUnusableBasePath(t *testing.T) {
	conf := &config.Config{}
	conf.Listen.BasePath = "{shop}"

	_, err := New(conf, config.DefaultSiteSettings(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), &routerHandler{})
	if err == nil {
		t.Fatal("New() with an unusable base path should fail")
	}
}

// The UA regression, end to end: its Zoho function posts the Sales Order id as a bare JSON number.
// The id has to reach Core.UpdateOrder with all 18 digits intact - the envelope's Data is an
// interface{}, and decoding that number as a float64 anywhere along the way would deliver
// 739178000064455000 and silently update nothing.
func TestRouter_NumericZohoIdSurvivesTheEnvelope(t *testing.T) {
	handler := &routerHandler{token: "secret"}
	conf := &config.Config{}
	conf.Listen.BasePath = ""
	server, err := New(conf, config.DefaultSiteSettings(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), handler)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body := `{"method":"order.status.update","data":{"zoho_id":739178000064455061,` +
		`"status":"Відправлено","grand_total":2722.51,"coupon":"CHILLAX10","ordered_items":[` +
		`{"zoho_id":"739178000063933582","price":195,"total":175.5,"quantity":1}]}}`

	req := httptest.NewRequest(http.MethodPost, "/zoho/webhook/order", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if len(handler.updated) != 1 {
		t.Fatalf("UpdateOrder called %d time(s), want 1", len(handler.updated))
	}
	if got := handler.updated[0].ZohoID; got != "739178000064455061" {
		t.Errorf("zoho_id = %q, want 739178000064455061", got)
	}
	if got := handler.updated[0].OrderedItems[0].ZohoID; got != "739178000063933582" {
		t.Errorf("item zoho_id = %q, want 739178000063933582", got)
	}
}

// Every error body carries a machine-readable code, the fallback handlers included: a caller that
// switches on response.error.code must not have to special-case a 404 or a 405 that arrived with
// nothing but prose.
func TestRouter_FallbackErrorsCarryACode(t *testing.T) {
	server := testServer(t, "")

	tests := []struct {
		name   string
		method string
		path   string
		status int
		code   string
	}{
		{"unrouted path", http.MethodGet, "/nope", http.StatusNotFound, "NOT_FOUND"},
		{"wrong method", http.MethodDelete, "/zoho/health", http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, server, tt.method, tt.path, "secret")
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d\nbody: %s", rec.Code, tt.status, rec.Body.String())
			}

			var body struct {
				Success bool `json:"success"`
				Error   *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %v (%s)", err, rec.Body.String())
			}
			if body.Success {
				t.Error("success = true on an error response")
			}
			if body.Error == nil {
				t.Fatalf("no error object in body: %s", rec.Body.String())
			}
			if body.Error.Code != tt.code {
				t.Errorf("error.code = %q, want %q", body.Error.Code, tt.code)
			}
			if body.Error.Message == "" {
				t.Error("error.message is empty")
			}
		})
	}
}
