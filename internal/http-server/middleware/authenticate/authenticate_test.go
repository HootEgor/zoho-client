package authenticate

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"zohoclient/entity"
)

// MockAuth implements the Authenticate interface for testing
type MockAuth struct {
	validTokens map[string]string // token -> username
}

func (m *MockAuth) AuthenticateByToken(token string) (*entity.UserAuth, error) {
	if username, ok := m.validTokens[token]; ok {
		return &entity.UserAuth{Name: username}, nil
	}
	return nil, nil
}

func TestAuthenticate_TokenExtraction(t *testing.T) {
	tests := []struct {
		name           string
		authHeader     string
		validTokens    map[string]string
		expectedStatus int
		expectedUser   string
	}{
		{
			name:           "no authorization header",
			authHeader:     "",
			validTokens:    map[string]string{"valid-token": "testuser"},
			expectedStatus: http.StatusUnauthorized,
			expectedUser:   "",
		},
		{
			name:           "valid bearer token",
			authHeader:     "Bearer valid-token",
			validTokens:    map[string]string{"valid-token": "testuser"},
			expectedStatus: http.StatusOK,
			expectedUser:   "testuser",
		},
		{
			name:           "bearer without token",
			authHeader:     "Bearer ",
			validTokens:    map[string]string{"valid-token": "testuser"},
			expectedStatus: http.StatusUnauthorized,
			expectedUser:   "",
		},
		{
			name:           "bearer only",
			authHeader:     "Bearer",
			validTokens:    map[string]string{"valid-token": "testuser"},
			expectedStatus: http.StatusUnauthorized,
			expectedUser:   "",
		},
		{
			name:           "missing bearer prefix",
			authHeader:     "valid-token",
			validTokens:    map[string]string{"valid-token": "testuser"},
			expectedStatus: http.StatusUnauthorized,
			expectedUser:   "",
		},
		{
			name:           "wrong prefix",
			authHeader:     "Basic valid-token",
			validTokens:    map[string]string{"valid-token": "testuser"},
			expectedStatus: http.StatusUnauthorized,
			expectedUser:   "",
		},
		{
			name:           "invalid token",
			authHeader:     "Bearer invalid-token",
			validTokens:    map[string]string{"valid-token": "testuser"},
			expectedStatus: http.StatusUnauthorized,
			expectedUser:   "",
		},
		{
			name:           "lowercase bearer rejected",
			authHeader:     "bearer valid-token",
			validTokens:    map[string]string{"valid-token": "testuser"},
			expectedStatus: http.StatusUnauthorized,
			expectedUser:   "",
		},
		{
			name:           "extra spaces in token",
			authHeader:     "Bearer  valid-token",
			validTokens:    map[string]string{" valid-token": "testuser"},
			expectedStatus: http.StatusOK,
			expectedUser:   "testuser",
		},
		{
			name:           "token with special characters",
			authHeader:     "Bearer abc123-def456_xyz",
			validTokens:    map[string]string{"abc123-def456_xyz": "testuser"},
			expectedStatus: http.StatusOK,
			expectedUser:   "testuser",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock auth
			mockAuth := &MockAuth{validTokens: tt.validTokens}

			// Create a test handler that just returns 200
			var capturedUser string
			testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedUser = w.Header().Get("X-User")
				w.WriteHeader(http.StatusOK)
			})

			// Create the middleware with discard logger
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			middleware := New(logger, mockAuth)
			handler := middleware(testHandler)

			// Create request
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			// Execute request
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			// Check status
			if rec.Code != tt.expectedStatus {
				t.Errorf("Status = %d, want %d", rec.Code, tt.expectedStatus)
			}

			// Check user if expected
			if tt.expectedUser != "" && capturedUser != tt.expectedUser {
				t.Errorf("User = %q, want %q", capturedUser, tt.expectedUser)
			}
		})
	}
}

func TestAuthenticate_OPTIONSBypass(t *testing.T) {
	// OPTIONS requests should bypass authentication (CORS preflight)
	mockAuth := &MockAuth{validTokens: map[string]string{}}

	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	middleware := New(logger, mockAuth)
	handler := middleware(testHandler)

	// Create OPTIONS request without auth header
	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("OPTIONS request should pass, got status %d", rec.Code)
	}

	if !handlerCalled {
		t.Error("Handler should be called for OPTIONS request")
	}
}

func TestAuthenticate_NilAuth(t *testing.T) {
	// nil auth should return unauthorized
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	middleware := New(logger, nil)
	handler := middleware(testHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Should return unauthorized when auth is nil, got %d", rec.Code)
	}
}
