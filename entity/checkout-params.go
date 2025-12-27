package entity

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"zohoclient/internal/lib/validate"

	"github.com/biter777/countries"
)

type Source string

const (
	SourceOpenCart  Source = "opencart"
	ShippingItemUid        = "cd3cc23c-6dfb-11ec-b75f-00155d018000"
)

type CheckoutParams struct {
	ClientDetails *ClientDetails `json:"client_details" bson:"client_details" validate:"required"`
	LineItems     []*LineItem    `json:"line_items" bson:"line_items" validate:"required,min=1,dive"`
	Total         float64        `json:"total" bson:"total" validate:"required,min=1"`
	ShippingTitle string         `json:"shipping_title,omitempty" bson:"shipping_title,omitempty"`
	Shipping      float64        `json:"shipping,omitempty" bson:"shipping,omitempty"`
	CouponTitle   string         `json:"coupon_title,omitempty" bson:"coupon_title,omitempty"`
	Coupon        float64        `json:"coupon,omitempty" bson:"coupon,omitempty"`
	TaxTitle      string         `json:"tax_title" bson:"tax_title"`
	TaxValue      float64        `json:"tax_value" bson:"tax_value"`
	Currency      string         `json:"currency" bson:"currency" validate:"required,oneof=PLN EUR"`
	CurrencyValue float64        `json:"currency_value,omitempty" bson:"currency_value,omitempty"`
	OrderId       int64          `json:"order_id" bson:"order_id" validate:"required"`
	Created       time.Time      `json:"created" bson:"created"`
	Status        string         `json:"status" bson:"status"`
	StatusId      int            `json:"status_id,omitempty" bson:"status_id,omitempty"`
	InvoiceId     string         `json:"invoice_id,omitempty" bson:"invoice_id,omitempty"`
	InvoiceFile   string         `json:"invoice_file,omitempty" bson:"invoice_file,omitempty"`
	ProformaId    string         `json:"proforma_id,omitempty" bson:"proforma_id,omitempty"`
	ProformaFile  string         `json:"proforma_file,omitempty" bson:"proforma_file,omitempty"`
	Source        Source         `json:"source,omitempty" bson:"source"`
	Comment       string         `json:"comment,omitempty" bson:"comment,omitempty"`
}

func (c *CheckoutParams) Bind(_ *http.Request) error {
	c.Created = time.Now()
	return validate.Struct(c)
}

func (c *CheckoutParams) Validate() error {
	if len(c.LineItems) == 0 {
		return fmt.Errorf("no line items")
	}
	if c.ClientDetails == nil {
		return fmt.Errorf("no client details")
	}
	return nil
}

// TaxRate calculates the tax rate as a percentage based on the tax value and total amount. Returns 0 if not applicable.
func (c *CheckoutParams) TaxRate() float64 {
	if c.TaxValue == 0 || c.Total <= c.TaxValue {
		return 0.0
	}
	return c.TaxValue * 100 / ((c.Total - c.Shipping) - c.TaxValue)
}

// Discount calculates the discount applied to the order.
// Base total = sum of LineItem.Total (without tax).
// Discount = Base total - (Total - TaxValue - Shipping).
// Returns: discount value, discount percentage (e.g., 10.0 for 10%).
func (c *CheckoutParams) Discount() (float64, float64) {
	var baseTotal float64
	for _, item := range c.LineItems {
		baseTotal += item.Total
	}
	if baseTotal == 0 {
		return 0, 0
	}
	actualTotal := c.Total - c.TaxValue - c.Shipping
	discount := baseTotal - actualTotal
	percent := (discount / baseTotal) * 100
	return discount, percent
}

type LineItem struct {
	Name   string  `json:"name" validate:"required"`
	Id     int64   `json:"id,omitempty" bson:"id"`
	Uid    string  `json:"uid,omitempty" bson:"uid"`
	ZohoId string  `json:"zoho_id,omitempty" bson:"zoho_id"`
	Qty    float64 `json:"qty" validate:"required,min=1"`
	Price  float64 `json:"price" validate:"required,min=1"`
	Tax    float64 `json:"tax" validate:"required,min=1"`
	Total  float64 `json:"total" validate:"required,min=1"`
	Sku    string  `json:"sku,omitempty" bson:"sku"`
}

type ClientDetails struct {
	FirstName string `json:"first_name" bson:"first_name" validate:"required"`
	LastName  string `json:"last_name" bson:"last_name" validate:"required"`
	Email     string `json:"email" bson:"email" validate:"required,email"`
	Phone     string `json:"phone" bson:"phone"`
	Country   string `json:"country" bson:"country"`
	ZipCode   string `json:"zip_code" bson:"zip_code"`
	City      string `json:"city" bson:"city"`
	Street    string `json:"street" bson:"street"`
	TaxId     string `json:"tax_id" bson:"tax_id"`
	GroupId   int64  `json:"group_id" bson:"group_id"`
}

func (c *ClientDetails) IsB2B() bool {
	return c.GroupId == 6 || c.GroupId == 7 || c.GroupId == 16 || c.GroupId == 18 || c.GroupId == 19
}

func (c *ClientDetails) TrimSpaces() {
	c.FirstName = strings.TrimSpace(c.FirstName)
	c.LastName = strings.TrimSpace(c.LastName)
	c.Email = strings.TrimSpace(c.Email)
	c.Phone = strings.TrimSpace(c.Phone)
	c.Country = strings.TrimSpace(c.Country)
	c.ZipCode = strings.TrimSpace(c.ZipCode)
	c.City = strings.TrimSpace(c.City)
	c.Street = strings.TrimSpace(c.Street)
}

func (c *ClientDetails) CountryCode() string {
	if c.Country == "" {
		return ""
	}
	if len(c.Country) == 2 {
		return c.Country
	}
	country := countries.ByName(c.Country)
	code := country.Alpha2()
	if len(code) == 2 {
		return code
	}
	return ""
}

func (c *ClientDetails) NormalizeZipCode() string {
	// Проверка на формат 00-000
	match, _ := regexp.MatchString(`^\d{2}-\d{3}$`, c.ZipCode)
	if match {
		return c.ZipCode
	}

	// Достаем только цифры
	var digits strings.Builder
	for _, r := range c.ZipCode {
		if unicode.IsDigit(r) {
			digits.WriteRune(r)
		}
	}

	code := digits.String()

	// Дополняем/обрезаем до 5 символов
	if len(code) < 5 {
		code = strings.Repeat("0", 5-len(code)) + code
	} else if len(code) > 5 {
		code = code[:5]
	}

	// Преобразуем к виду 00-000
	return code[:2] + "-" + code[2:]
}

// ParseTaxId extracts a tax ID from a JSON-formatted string based on the given field ID and assigns it to the ClientDetails.
// Returns an error if the provided raw data is invalid JSON or the extraction fails.
// Raw string example: {"2":"DE362155758"}
func (c *ClientDetails) ParseTaxId(fieldId, raw string) error {
	if fieldId == "" || raw == "" {
		return nil
	}
	//var jsonStr string
	//if err := json.Unmarshal([]byte(raw), &jsonStr); err != nil {
	//	return err
	//}
	var data map[string]string
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return err
	}
	c.TaxId = data[fieldId]
	return nil
}
