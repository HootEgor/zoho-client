package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"zohoclient/entity"
	"zohoclient/internal/config"
)

// routerHandler embeds the Handler interface, so any method a routing test is not supposed to
// reach is nil and panics loudly rather than silently succeeding.
type routerHandler struct {
	Handler
	token string
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
