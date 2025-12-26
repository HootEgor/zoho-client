package entity

import (
	"net/http"
	"zohoclient/internal/lib/validate"
)

type ApiOrder struct {
	ZohoID       string           `json:"zoho_id" validate:"required"`
	Status       string           `json:"status" validate:"required"`
	GrandTotal   float64          `json:"grand_total" validate:"required"`
	OrderedItems []ApiOrderedItem `json:"ordered_items" validate:"required,dive"`
}

type ApiOrderedItem struct {
	ZohoID   string  `json:"zoho_id" validate:"required"`
	Price    float64 `json:"price" validate:"required,min=0.01"`
	Total    float64 `json:"total" validate:"required,min=0.01"`
	Quantity int     `json:"quantity" validate:"required,min=1"`
}

func (o *ApiOrder) Bind(_ *http.Request) error {
	return validate.Struct(o)
}

func (i *ApiOrderedItem) Bind(_ *http.Request) error {
	return validate.Struct(i)
}
