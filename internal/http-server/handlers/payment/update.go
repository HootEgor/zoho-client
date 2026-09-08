package payment

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"zohoclient/entity"
	"zohoclient/internal/lib/api/request"
	"zohoclient/internal/lib/api/response"
	apierrors "zohoclient/internal/lib/errors"

	"github.com/go-chi/render"
)

// Update receives the Payments list of a Sales Order from Zoho. While the feature only records
// what arrives, the raw body is logged on every call, not just on a decode failure: the point of
// this stage is to see exactly what each shop's Deluge function sends.
func Update(logger *slog.Logger, core Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const op = "handlers.payment.Update"

		log := logger.With(
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote_addr", r.RemoteAddr),
			slog.String("op", op),
		)

		req, body, err := request.DecodeWithBody(r)
		if err != nil {
			if errors.Is(err, request.ErrEmptyBody) {
				apiErr := apierrors.NewBadRequestError("Empty request body")
				log.Warn("request body is empty", slog.String("error_code", string(apiErr.Code)))
				w.WriteHeader(apiErr.HTTPStatus)
				render.JSON(w, r, response.ErrorFromAPIError(apiErr))
				return
			}
			apiErr := apierrors.NewBadRequestError("Invalid request format")
			log.Warn("failed to decode request",
				slog.String("error", err.Error()),
				slog.String("payload", request.Snippet(body)),
				slog.String("error_code", string(apiErr.Code)),
			)
			w.WriteHeader(apiErr.HTTPStatus)
			render.JSON(w, r, response.ErrorFromAPIError(apiErr))
			return
		}

		log.Debug("payments webhook received", slog.String("payload", request.Snippet(body)))

		var updates []entity.ApiPaymentUpdate
		err = request.DecodeAndValidateArrayData(req, r, &updates)
		if err != nil {
			apiErr := apierrors.NewValidationError("Invalid payments data")
			log.Warn("failed to decode payments data",
				slog.String("error", err.Error()),
				slog.String("payload", request.Snippet(body)),
				slog.String("error_code", string(apiErr.Code)),
			)
			w.WriteHeader(apiErr.HTTPStatus)
			render.JSON(w, r, response.ErrorFromAPIError(apiErr))
			return
		}

		if len(updates) == 0 {
			render.JSON(w, r, response.OkWithMessage("No payments provided", "success"))
			return
		}

		recorded := 0
		for i := range updates {
			if err := core.UpdatePayments(&updates[i]); err != nil {
				apiErr := apierrors.NewDatabaseError("UpdatePayments")
				log.Error("failed to record payments update",
					slog.Int("index", i),
					slog.String("zoho_id", updates[i].ZohoID),
					slog.Int("applied", i),
					slog.Int("total", len(updates)),
					slog.String("error", err.Error()),
					slog.String("error_code", string(apiErr.Code)),
				)
				w.WriteHeader(apiErr.HTTPStatus)
				render.JSON(w, r, response.ErrorFromAPIError(apiErr))
				return
			}
			recorded += len(updates[i].Payments)
		}

		render.JSON(w, r, response.OkWithMessage(
			fmt.Sprintf("%d payment(s) recorded", recorded), "success"))
	}
}
