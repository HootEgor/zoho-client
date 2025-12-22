package api

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"zohoclient/internal/config"
	"zohoclient/internal/http-server/handlers/errors"
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
	httpServer *http.Server
	log        *slog.Logger
}

type Handler interface {
	authenticate.Authenticate
	order.Core
}

func New(conf *config.Config, log *slog.Logger, handler Handler) error {

	server := Server{
		conf: conf,
		log:  log.With(sl.Module("api.server")),
	}

	router := chi.NewRouter()
	router.Use(timeout.Timeout(5))
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Use(render.SetContentType(render.ContentTypeJSON))
	router.Use(authenticate.New(log, handler))

	router.NotFound(errors.NotFound(log))
	router.MethodNotAllowed(errors.NotAllowed(log))

	router.Route("/zoho", func(v1 chi.Router) {
		v1.Route("/webhook", func(webhook chi.Router) {
			webhook.Route("/order", func(r chi.Router) {
				r.Post("/", order.UpdateOrder(log, handler))
			})
		})
	})

	httpLog := slog.NewLogLogger(log.Handler(), slog.LevelError)
	server.httpServer = &http.Server{
		Handler:  router,
		ErrorLog: httpLog,
	}

	serverAddress := fmt.Sprintf("%s:%s", conf.Listen.BindIP, conf.Listen.Port)
	listener, err := net.Listen("tcp", serverAddress)
	if err != nil {
		return err
	}

	server.log.Info("starting api server", slog.String("address", serverAddress))

	return server.httpServer.Serve(listener)
}
