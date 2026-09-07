package errors

import (
	"log/slog"
	"net/http"
	"zohoclient/internal/lib/api/response"
	apierrors "zohoclient/internal/lib/errors"

	"github.com/go-chi/render"
)

func WrongData(_ *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		//mod := sl.Module("http.handlers.errors")

		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, response.ErrorWithCode(
			string(apierrors.ErrCodeBadRequest), "Wrong data"))
	}
}
