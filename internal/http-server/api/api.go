package api

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
	"zohoclient/internal/config"
	"zohoclient/internal/http-server/handlers/b2b"
	"zohoclient/internal/http-server/handlers/errors"
	"zohoclient/internal/http-server/handlers/health"
	"zohoclient/internal/http-server/handlers/order"
	"zohoclient/internal/http-server/middleware/authenticate"
	"zohoclient/internal/http-server/middleware/timeout"
	"zohoclient/internal/lib/sl"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/render"
)

type Server struct {
	conf       *config.Config
	site       *config.SiteSettings
	httpServer *http.Server
	log        *slog.Logger
}

type Handler interface {
	authenticate.Authenticate
	order.Core
	b2b.Core
	health.Core
}

func New(conf *config.Config, site *config.SiteSettings, log *slog.Logger, handler Handler) (*Server, error) {
	server := &Server{
		conf: conf,
		site: site,
		log:  log.With(sl.Module("api.server")),
	}

	router := chi.NewRouter()
	router.Use(timeout.Timeout(5))
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Use(render.SetContentType(render.ContentTypeJSON))

	router.NotFound(errors.NotFound(log))
	router.MethodNotAllowed(errors.NotAllowed(log))

	// The liveness probe is the one route outside authentication: a load balancer or a systemd
	// watchdog has no token to present. It answers with a state and an uptime and nothing else —
	// everything that identifies this shop or its traffic is behind the token, on /zoho/status.
	router.Get("/health", health.Check(log, handler))

	// Everything else requires the Bearer token. The group scopes the middleware so adding a
	// route below cannot accidentally publish it unauthenticated.
	router.Group(func(v1 chi.Router) {
		v1.Use(authenticate.New(log, handler))

		v1.Route("/zoho", func(v1 chi.Router) {
			v1.Get("/status", health.Status(log, handler))

			v1.Route("/webhook", func(webhook chi.Router) {
				webhook.Route("/order", func(r chi.Router) {
					r.Post("/", order.UpdateOrder(log, handler))
				})
				// The B2B portal webhook feeds the Deals pipeline; a site that does not run the
				// B2B flow has no route for it at all, so a stray call 404s rather than creating
				// a Deal nobody will look at.
				if site.B2B {
					webhook.Route("/b2b", func(r chi.Router) {
						r.Post("/", b2b.Webhook(log, handler))
					})
				}
			})
			v1.Route("/push", func(push chi.Router) {
				push.Route("/order", func(r chi.Router) {
					r.Get("/{id}", order.PushOrder(log, handler))
				})
			})
		})
	})

	httpLog := slog.NewLogLogger(log.Handler(), slog.LevelError)
	server.httpServer = &http.Server{
		Handler:  router,
		ErrorLog: httpLog,
	}

	return server, nil
}

func (s *Server) Start() error {
	serverAddress := fmt.Sprintf("%s:%s", s.conf.Listen.BindIP, s.conf.Listen.Port)
	listener, err := net.Listen("tcp", serverAddress)
	if err != nil {
		return err
	}

	s.log.Info("starting api server", slog.String("address", serverAddress))

	return s.httpServer.Serve(listener)
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.log.Info("shutting down api server")
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) ShutdownWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.Shutdown(ctx)
}
