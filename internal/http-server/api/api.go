package api

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
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
	basePath   string
	log        *slog.Logger
}

// DefaultBasePath is the namespace every endpoint lives under when listen.base_path is not set,
// which keeps an existing config file serving the exact paths it served before.
const DefaultBasePath = "zoho"

// basePathSegment accepts one path segment: letters, digits, dash, underscore and dot. It rejects
// chi's own "{param}" syntax and anything that would need escaping in a URL.
var basePathSegment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// NormalizeBasePath turns a configured base path into the rooted, slash-free form chi wants, and
// rejects one that cannot be mounted. An empty value means the default rather than the domain
// root: this service must never own "/", or two instances on one domain would collide there.
func NormalizeBasePath(configured string) (string, error) {
	trimmed := strings.Trim(strings.TrimSpace(configured), "/")
	if trimmed == "" {
		trimmed = DefaultBasePath
	}

	// Nesting is allowed ("shop/zoho"), so validate segment by segment. An empty segment means a
	// doubled slash, which would mount a route no client could address.
	for _, segment := range strings.Split(trimmed, "/") {
		if !basePathSegment.MatchString(segment) {
			return "", fmt.Errorf("invalid listen.base_path %q: %q is not a usable path segment",
				configured, segment)
		}
	}

	return "/" + trimmed, nil
}

// BasePath is the namespace every route is mounted under, rooted and without a trailing slash.
func (s *Server) BasePath() string {
	return s.basePath
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

	basePath, err := NormalizeBasePath(conf.Listen.BasePath)
	if err != nil {
		return nil, err
	}
	server.basePath = basePath

	router := chi.NewRouter()
	router.Use(timeout.Timeout(5))
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Use(render.SetContentType(render.ContentTypeJSON))

	router.NotFound(errors.NotFound(log))
	router.MethodNotAllowed(errors.NotAllowed(log))

	// Every route lives under the one configured namespace, health check included, so nothing this
	// process serves sits at the domain root. Two instances published on one domain set different
	// listen.base_path values and a reverse proxy tells them apart by path alone.
	router.Route(basePath, func(root chi.Router) {

		// The liveness probe is the one route outside authentication: a load balancer or a systemd
		// watchdog has no token to present. It answers with a state and an uptime and nothing
		// else — everything that identifies this shop or its traffic is behind the token, on
		// <base>/status.
		root.Get("/health", health.Check(log, handler))

		// Everything else requires the Bearer token. The group scopes the middleware so adding a
		// route below cannot accidentally publish it unauthenticated.
		root.Group(func(v1 chi.Router) {
			v1.Use(authenticate.New(log, handler))

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

	s.log.Info("starting api server",
		slog.String("address", serverAddress),
		slog.String("base_path", s.basePath))

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
