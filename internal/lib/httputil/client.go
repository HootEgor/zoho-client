package httputil

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	"zohoclient/internal/lib/sl"
)

// NewHTTPClient creates a pre-configured *http.Client with sensible defaults
// for external API communication: connection pooling, idle timeout, and a
// caller-specified request timeout.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

// CloseBody is a defer helper that closes an HTTP response body and logs
// a warning on failure. Use it as:
//
//	defer httputil.CloseBody(resp.Body, log)
func CloseBody(body io.ReadCloser, log *slog.Logger) {
	if err := body.Close(); err != nil {
		log.With(sl.Err(err)).Warn("failed to close response body")
	}
}
