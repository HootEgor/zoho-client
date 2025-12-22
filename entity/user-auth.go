package entity

import (
	"net/http"
	"zohoclient/internal/lib/validate"
)

type UserAuth struct {
	Name  string `json:"name" bson:"name" validate:"omitempty"`
	Token string `json:"token" bson:"token" validate:"required,min=1"`
}

func (u *UserAuth) Bind(_ *http.Request) error {
	return validate.Struct(u)
}
