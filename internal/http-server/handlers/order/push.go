package order

import (
	"log/slog"
	"net/http"
	"strconv"
	"zohoclient/internal/lib/api/response"
	apierrors "zohoclient/internal/lib/errors"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

func PushOrder(logger *slog.Logger, core Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const op = "handlers.order.PushOrder"

		log := logger.With(
			slog.String("op", op),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote_addr", r.RemoteAddr),
		)

		idParam := chi.URLParam(r, "id")
		if idParam == "" {
			apiErr := apierrors.NewBadRequestError("Order ID is required")
			log.Warn("missing order id parameter", slog.String("error_code", string(apiErr.Code)))
			w.WriteHeader(apiErr.HTTPStatus)
			render.JSON(w, r, response.ErrorFromAPIError(apiErr))
			return
		}

		orderId, err := strconv.ParseInt(idParam, 10, 64)
		if err != nil {
			apiErr := apierrors.NewBadRequestError("Invalid order ID format")
			log.Warn("invalid order id",
				slog.String("id", idParam),
				slog.String("error", err.Error()),
				slog.String("error_code", string(apiErr.Code)),
			)
			w.WriteHeader(apiErr.HTTPStatus)
			render.JSON(w, r, response.ErrorFromAPIError(apiErr))
			return
		}

		log = log.With(slog.Int64("order_id", orderId))

		zohoId, err := core.PushOrderToZoho(orderId)
		if err != nil {
			apiErr := apierrors.NewDatabaseError("PushOrderToZoho")
			log.Error("failed to push order to Zoho",
				slog.String("error", err.Error()),
				slog.String("error_code", string(apiErr.Code)),
			)
			w.WriteHeader(apiErr.HTTPStatus)
			render.JSON(w, r, response.ErrorFromAPIError(apiErr))
			return
		}

		//log.Info("order pushed to Zoho successfully", slog.String("zoho_id", zohoId))
		render.JSON(w, r, response.Ok(map[string]string{
			"zoho_id": zohoId,
		}))
	}
}
