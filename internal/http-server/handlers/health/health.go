package health

import (
	"log/slog"
	"net/http"
	"zohoclient/entity"
	"zohoclient/internal/lib/api/response"

	"github.com/go-chi/render"
)

// Check is the unauthenticated liveness/readiness probe. It answers the one question a load
// balancer or a monitoring check asks — is this process able to do its work — and deliberately
// says nothing else: the endpoint is reachable without a token, so it must not report which shop
// this is, how many orders are queued, or what the last error said. Status serves that, behind
// the same authentication as every other endpoint.
//
// It is mounted under the configured listen.base_path like every other route, so nothing this
// process serves sits at the domain root where a second instance would collide with it.
//
// 200 while every component is up or merely switched off, 503 once one is down.
func Check(logger *slog.Logger, core Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const op = "handlers.health.Check"

		status := core.Status()

		body := map[string]any{
			"status": status.Status,
			"uptime": status.Uptime,
		}

		if status.Status != entity.StatusOK {
			logger.With(
				slog.String("op", op),
				slog.String("status", status.Status),
				slog.Any("components", downComponents(status)),
			).Warn("health check reports a degraded service")

			w.WriteHeader(http.StatusServiceUnavailable)
			render.JSON(w, r, body)
			return
		}

		render.JSON(w, r, body)
	}
}

// Status is the authenticated, detailed report: component states with their reasons, the enabled
// features, and what the order poller last did. Unlike Check it always answers 200 — the caller
// asked for the report, and a degraded service is the answer, not a failure to produce one.
func Status(_ *slog.Logger, core Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		render.JSON(w, r, response.Ok(core.Status()))
	}
}

// downComponents names the components that made the service degraded, so the log line says what
// failed without carrying the whole report.
func downComponents(status entity.ServiceStatus) []string {
	var down []string
	for _, c := range status.Components {
		if c.State == entity.ComponentDown {
			down = append(down, c.Name+": "+c.Detail)
		}
	}
	return down
}
