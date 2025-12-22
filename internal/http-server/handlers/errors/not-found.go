package errors

import (
	"log/slog"
	"net/http"
	"zohoclient/internal/lib/api/response"

	"github.com/go-chi/render"
)

func NotFound(_ *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		//mod := sl.Module("http.handlers.errors")

		render.Status(r, 404)
		render.JSON(w, r, response.Error("Requested resource not found"))
	}
}
