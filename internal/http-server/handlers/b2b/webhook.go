package b2b

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"zohoclient/entity"
	"zohoclient/internal/lib/api/response"
	apierrors "zohoclient/internal/lib/errors"

	"github.com/go-chi/render"
)

func Webhook(logger *slog.Logger, core Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const op = "handlers.b2b.Webhook"

		log := logger.With(
			slog.String("op", op),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote_addr", r.RemoteAddr),
		)

		// Decode payload directly from request body
		var payload entity.B2BWebhookPayload
		err := json.NewDecoder(r.Body).Decode(&payload)
		if err != nil {
			if err == io.EOF {
				apiErr := apierrors.NewBadRequestError("Empty request body")
				log.Warn("request body is empty", slog.String("error_code", string(apiErr.Code)))
				w.WriteHeader(apiErr.HTTPStatus)
				render.JSON(w, r, response.ErrorFromAPIError(apiErr))
				return
			}
			apiErr := apierrors.NewBadRequestError("Invalid request format")
			log.Warn("failed to decode request",
				slog.String("error", err.Error()),
				slog.String("error_code", string(apiErr.Code)),
			)
			w.WriteHeader(apiErr.HTTPStatus)
			render.JSON(w, r, response.ErrorFromAPIError(apiErr))
			return
		}

		// Validate payload
		if err := payload.Bind(r); err != nil {
			apiErr := apierrors.NewValidationError("Invalid webhook payload")
			log.Warn("failed to validate webhook payload",
				slog.String("error", err.Error()),
				slog.String("error_code", string(apiErr.Code)),
			)
			w.WriteHeader(apiErr.HTTPStatus)
			render.JSON(w, r, response.ErrorFromAPIError(apiErr))
			return
		}

		log = log.With(
			slog.String("order_uid", payload.Data.OrderUID),
			slog.String("order_number", payload.Data.OrderNumber),
			slog.String("event", payload.Event),
		)

		zohoId, err := core.ProcessB2BWebhook(&payload)
		if err != nil {
			apiErr := apierrors.NewInternalError("Failed to process B2B webhook")
			log.Error("failed to process B2B webhook",
				slog.String("error", err.Error()),
				slog.String("error_code", string(apiErr.Code)),
			)
			w.WriteHeader(apiErr.HTTPStatus)
			render.JSON(w, r, response.ErrorFromAPIError(apiErr))
			return
		}

		log.Info("B2B webhook processed successfully", slog.String("zoho_id", zohoId))
		render.JSON(w, r, response.Ok(map[string]string{
			"zoho_id": zohoId,
		}))
	}
}
