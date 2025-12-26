package entity

import (
	"net/http"
	"zohoclient/internal/lib/validate"
)

type ApiOrder struct {
	ZohoID       string           `json:"zoho_id" validation:"required"`
	Status       string           `json:"status" validation:"required"`
	GrandTotal   float64          `json:"grand_total" validation:"required"`
	OrderedItems []ApiOrderedItem `json:"ordered_items" validation:"required,dive"`
}

type ApiOrderedItem struct {
	ZohoID   string  `json:"zoho_id" validation:"required"`
	Price    float64 `json:"price" validation:"required,min=0.01"`
	Total    float64 `json:"total" validation:"required,min=0.01"`
	Quantity int     `json:"quantity" validation:"required,min=1"`
}

func (o *ApiOrder) Bind(_ *http.Request) error {
	return validate.Struct(o)
}

func (i *ApiOrderedItem) Bind(_ *http.Request) error {
	return validate.Struct(i)
}
