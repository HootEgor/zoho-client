package entity

import (
	"net/http"
	"zohoclient/internal/lib/validate"
)

type ApiOrder struct {
	ZohoID       string           `json:"zoho_id" validate:"required"`
	Status       string           `json:"status" validate:"required"`
	GrandTotal   float64          `json:"grand_total" validate:"gt=0"`
	OrderedItems []ApiOrderedItem `json:"ordered_items" validate:"required,dive"`
}

type ApiOrderedItem struct {
	ZohoID   string  `json:"zoho_id" validate:"required"`
	Price    float64 `json:"price" validate:"gt=0"`
	Total    float64 `json:"total" validate:"gt=0"`
	Quantity int     `json:"quantity" validate:"gt=0"`
	Shipping bool    `json:"is_shipping"`
}

func (o *ApiOrder) Bind(_ *http.Request) error {
	return validate.Struct(o)
}
