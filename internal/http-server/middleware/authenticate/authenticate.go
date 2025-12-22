package authenticate

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/lib/api/cont"
	"zohoclient/internal/lib/api/response"
	"zohoclient/internal/lib/sl"
	"zohoclient/internal/lib/util"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/render"
)

type Authenticate interface {
	AuthenticateByToken(token string) (*entity.UserAuth, error)
}

func New(log *slog.Logger, auth Authenticate) func(next http.Handler) http.Handler {
	mod := sl.Module("middleware.authenticate")
	log.With(mod).Info("authenticate middleware initialized")

	return func(next http.Handler) http.Handler {

		fn := func(w http.ResponseWriter, r *http.Request) {
			// Allow OPTIONS requests (CORS preflight) without authentication
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			id := middleware.GetReqID(r.Context())
			remote := util.ExtractIPAddress(r.RemoteAddr, r.Header.Get("X-Forwarded-For"))
			logger := log.With(
				mod,
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("remote_addr", remote),
				slog.String("request_id", id),
			)
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			t1 := time.Now()
			defer func() {
				logger.With(
					slog.Int("status", ww.Status()),
					slog.Int("size", ww.BytesWritten()),
					slog.Float64("duration", time.Since(t1).Seconds()),
				).Info("incoming request")
			}()

			token := ""
			header := r.Header.Get("Authorization")
			if len(header) == 0 {
				logger = logger.With(sl.Err(fmt.Errorf("authorization header not found")))
				authFailed(ww, r, "Authorization header not found")
				return
			}
			if strings.Contains(header, "Bearer") {
				token = strings.Split(header, " ")[1]
			}
			if len(token) == 0 {
				logger = logger.With(sl.Err(fmt.Errorf("token not found")))
				authFailed(ww, r, "Token not found")
				return
			}
			logger = logger.With(sl.Secret("token", token))

			if auth == nil {
				authFailed(ww, r, "Unauthorized: authentication not enabled")
				return
			}

			user, err := auth.AuthenticateByToken(token)
			if err != nil {
				logger = logger.With(sl.Err(err))
				authFailed(ww, r, "Unauthorized: token not found")
				return
			}
			logger = logger.With(
				slog.String("user", user.Name),
			)
			ctx := cont.PutUser(r.Context(), user)

			ww.Header().Set("X-Request-ID", id)
			ww.Header().Set("X-User", user.Name)
			next.ServeHTTP(ww, r.WithContext(ctx))
		}

		return http.HandlerFunc(fn)
	}
}

func authFailed(w http.ResponseWriter, r *http.Request, message string) {
	render.Status(r, http.StatusUnauthorized)
	render.JSON(w, r, response.Error(message))
}
