package errors

import (
	"log/slog"
	"net/http"
	"zohoclient/internal/lib/api/response"

	"github.com/go-chi/render"
)

func WrongData(_ *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		//mod := sl.Module("http.handlers.errors")

		render.Status(r, 400)
		render.JSON(w, r, response.Error("Wrong data"))
	}
}
