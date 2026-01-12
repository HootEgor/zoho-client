package entity

import (
	"net/http"
	"time"
	"zohoclient/internal/lib/validate"
)

// B2BWebhookPayload represents the incoming webhook from B2B portal
type B2BWebhookPayload struct {
	Event     string          `json:"event" validate:"required,eq=order_confirmed"`
	Timestamp time.Time       `json:"timestamp"`
	Data      B2BWebhookOrder `json:"data" validate:"required"`
}

func (p *B2BWebhookPayload) Bind(_ *http.Request) error {
	return validate.Struct(p)
}

// B2BWebhookOrder contains the order data from webhook
type B2BWebhookOrder struct {
	OrderUID        string           `json:"order_uid" validate:"required"`
	OrderNumber     string           `json:"order_number" validate:"required"`
	ClientUID       string           `json:"client_uid" validate:"required"`
	ClientName      string           `json:"client_name"`
	ClientEmail     string           `json:"client_email"`
	ClientPhone     string           `json:"client_phone"`
	ClientCountry   string           `json:"client_country"`
	ClientCity      string           `json:"client_city"`
	ClientStreet    string           `json:"client_street"`
	ClientZipCode   string           `json:"client_zip_code"`
	ClientTaxID     string           `json:"client_tax_id"`
	StoreUID        string           `json:"store_uid"`
	Status          string           `json:"status"`
	Total           float64          `json:"total" validate:"gt=0"`
	Subtotal        float64          `json:"subtotal"`
	TotalVAT        float64          `json:"total_vat"`
	DiscountPercent float64          `json:"discount_percent"`
	DiscountAmount  float64          `json:"discount_amount"`
	CurrencyCode    string           `json:"currency_code" validate:"required,oneof=USD EUR PLN UAH"`
	ShippingAddress string           `json:"shipping_address"`
	Comment         string           `json:"comment"`
	CreatedAt       time.Time        `json:"created_at"`
	Items           []B2BWebhookItem `json:"items" validate:"required,min=1,dive"`
}

// B2BWebhookItem represents a single line item in the webhook
type B2BWebhookItem struct {
	ProductUID    string  `json:"product_uid" validate:"required"`
	ProductSKU    string  `json:"product_sku"`
	Quantity      int     `json:"quantity" validate:"required,gt=0"`
	Price         float64 `json:"price" validate:"gt=0"`
	Discount      float64 `json:"discount"`
	PriceDiscount float64 `json:"price_discount"`
	Tax           float64 `json:"tax"`
	Total         float64 `json:"total" validate:"gt=0"`
}
