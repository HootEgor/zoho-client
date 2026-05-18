package order

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

func UpdateOrder(logger *slog.Logger, order Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const op = "handlers.order.UpdateOrder"

		// Setup logging with request ID
		log := logger.With("request received",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote_addr", r.RemoteAddr),
			slog.String("op", op),
		)

		req, err := request.Decode(r)
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
				slog.String("error_code", string(apiErr.Code)),
			)
			w.WriteHeader(apiErr.HTTPStatus)
			render.JSON(w, r, response.ErrorFromAPIError(apiErr))
			return
		}

		// Extract status updates array
		var updates []entity.ApiOrder
		err = request.DecodeAndValidateArrayData(req, r, &updates)
		if err != nil {
			apiErr := apierrors.NewValidationError("Invalid order updates data")
			log.Warn("failed to decode order updates data",
				slog.String("error", err.Error()),
				slog.String("error_code", string(apiErr.Code)),
			)
			w.WriteHeader(apiErr.HTTPStatus)
			render.JSON(w, r, response.ErrorFromAPIError(apiErr))
			return
		}

		if len(updates) == 0 {
			render.JSON(w, r, response.OkWithMessage("No updates provided", "success"))
			return
		}

		// Apply each update sequentially. Fail-fast on the first error so the caller
		// (Zoho webhook function) can retry the batch; earlier successful updates are
		// idempotent thanks to Modified_Time echo suppression in core.UpdateOrder.
		for i := range updates {
			if err := order.UpdateOrder(&updates[i]); err != nil {
				apiErr := apierrors.NewDatabaseError("UpdateOrder")
				log.Error("failed to update order",
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
		}

		render.JSON(w, r, response.OkWithMessage(
			fmt.Sprintf("%d order(s) updated successfully", len(updates)), "success"))
	}
}
