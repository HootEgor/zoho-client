package errors

import (
	"log/slog"
	"net/http"
	"zohoclient/internal/lib/api/response"
	apierrors "zohoclient/internal/lib/errors"

	"github.com/go-chi/render"
)

func NotFound(_ *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		//mod := sl.Module("http.handlers.errors")

		render.Status(r, http.StatusNotFound)
		render.JSON(w, r, response.ErrorWithCode(
			string(apierrors.ErrCodeNotFound), "Requested resource not found"))
	}
}
