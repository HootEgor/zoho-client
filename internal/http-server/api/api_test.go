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

func testServer(t *testing.T) (*Server, *routerHandler) {
	t.Helper()

	handler := &routerHandler{token: "secret"}
	conf := &config.Config{}
	server, err := New(conf, config.DefaultSiteSettings(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), handler)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return server, handler
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
	server, _ := testServer(t)

	rec := do(t, server, http.MethodGet, "/health", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d\nbody: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// Everything the probe does not disclose stays behind the token — including the detailed report.
func TestRouter_StatusRequiresToken(t *testing.T) {
	server, _ := testServer(t)

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
	server, _ := testServer(t)

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
