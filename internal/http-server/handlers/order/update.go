package order

import (
	"errors"
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

		err = order.UpdateOrder(&updates[0])
		if err != nil {
			apiErr := apierrors.NewDatabaseError("UpdateOrder")
			log.Error("failed to update order",
				slog.String("error", err.Error()),
				slog.String("error_code", string(apiErr.Code)),
			)
			w.WriteHeader(apiErr.HTTPStatus)
			render.JSON(w, r, response.ErrorFromAPIError(apiErr))
			return
		}

		render.JSON(w, r, response.OkWithMessage("Order updated successfully", "success"))
	}
}
