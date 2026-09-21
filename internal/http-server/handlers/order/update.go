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

		// Extract status updates array
		var updates []entity.ApiOrder
		err = request.DecodeAndValidateArrayData(req, r, &updates)
		if err != nil {
			apiErr := apierrors.NewValidationError("Invalid order updates data")
			log.Warn("failed to decode order updates data",
				slog.String("error", err.Error()),
				slog.String("payload", request.Snippet(body)),
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
			err := order.UpdateOrder(&updates[i])
			if err == nil {
				continue
			}

			// This is the only place a failed update is reported - core.UpdateOrder returns
			// without logging so one condition produces one line.
			//
			// A webhook for an order this instance does not carry is an ordinary outcome, not a
			// fault: Zoho holds orders from both shops and from before this service synced
			// anything. So it is a 404 and a warning rather than a 500 and an error - the row is
			// genuinely absent, the database answered perfectly well, and calling it a
			// DATABASE_ERROR told the caller to retry something no retry can find. Zoho gets a
			// verdict it can act on, and a log filtered to errors shows faults, not routine
			// traffic.
			notFound := errors.Is(err, entity.ErrOrderNotFound)

			apiErr := apierrors.NewDatabaseError("UpdateOrder")
			if notFound {
				apiErr = apierrors.NewNotFoundErrorWithID("Order", updates[i].ZohoID)
			}

			fields := []any{
				slog.Int("index", i),
				slog.String("zoho_id", updates[i].ZohoID),
				slog.Int("applied", i),
				slog.Int("total", len(updates)),
				slog.String("error", err.Error()),
				slog.String("error_code", string(apiErr.Code)),
			}

			if notFound {
				log.Warn("order not found, dropping update", fields...)
			} else {
				log.Error("failed to update order", fields...)
			}

			w.WriteHeader(apiErr.HTTPStatus)
			render.JSON(w, r, response.ErrorFromAPIError(apiErr))
			return
		}

		render.JSON(w, r, response.OkWithMessage(
			fmt.Sprintf("%d order(s) updated successfully", len(updates)), "success"))
	}
}
