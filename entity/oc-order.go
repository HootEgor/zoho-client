package entity

import "time"

type OCOrder struct {
	OrderID               int64     `json:"order_id"`
	InvoiceNo             string    `json:"invoice_no"`
	InvoicePrefix         string    `json:"invoice_prefix"`
	StoreID               int64     `json:"store_id"`
	StoreName             string    `json:"store_name"`
	StoreURL              string    `json:"store_url"`
	CustomerID            int64     `json:"customer_id"`
	CustomerGroupID       int64     `json:"customer_group_id"`
	Firstname             string    `json:"firstname"`
	Lastname              string    `json:"lastname"`
	Email                 string    `json:"email"`
	Telephone             string    `json:"telephone"`
	CustomField           string    `json:"custom_field"`
	PaymentFirstname      string    `json:"payment_firstname"`
	PaymentLastname       string    `json:"payment_lastname"`
	PaymentCompany        string    `json:"payment_company"`
	PaymentAddress1       string    `json:"payment_address_1"`
	PaymentAddress2       string    `json:"payment_address_2"`
	PaymentCity           string    `json:"payment_city"`
	PaymentPostcode       string    `json:"payment_postcode"`
	PaymentCountry        string    `json:"payment_country"`
	PaymentCountryID      int64     `json:"payment_country_id"`
	PaymentZone           string    `json:"payment_zone"`
	PaymentZoneID         int64     `json:"payment_zone_id"`
	PaymentAddressFormat  string    `json:"payment_address_format"`
	PaymentCustomField    string    `json:"payment_custom_field"`
	PaymentMethod         string    `json:"payment_method"`
	PaymentCode           string    `json:"payment_code"`
	ShippingFirstname     string    `json:"shipping_firstname"`
	ShippingLastname      string    `json:"shipping_lastname"`
	ShippingCompany       string    `json:"shipping_company"`
	ShippingAddress1      string    `json:"shipping_address_1"`
	ShippingAddress2      string    `json:"shipping_address_2"`
	ShippingCity          string    `json:"shipping_city"`
	ShippingPostcode      string    `json:"shipping_postcode"`
	ShippingCountry       string    `json:"shipping_country"`
	ShippingCountryID     int64     `json:"shipping_country_id"`
	ShippingZone          string    `json:"shipping_zone"`
	ShippingZoneID        int64     `json:"shipping_zone_id"`
	ShippingAddressFormat string    `json:"shipping_address_format"`
	ShippingCustomField   string    `json:"shipping_custom_field"`
	ShippingMethod        string    `json:"shipping_method"`
	ShippingCode          string    `json:"shipping_code"`
	Comment               string    `json:"comment"`
	Total                 float64   `json:"total"`
	OrderStatusID         int64     `json:"order_status_id"`
	AffiliateID           int64     `json:"affiliate_id"`
	Commission            float64   `json:"commission"`
	MarketingID           int64     `json:"marketing_id"`
	Tracking              string    `json:"tracking"`
	LanguageID            int64     `json:"language_id"`
	CurrencyID            int64     `json:"currency_id"`
	CurrencyCode          string    `json:"currency_code"`
	CurrencyValue         float64   `json:"currency_value"`
	IP                    string    `json:"ip"`
	ForwardedIP           string    `json:"forwarded_ip"`
	UserAgent             string    `json:"user_agent"`
	AcceptLanguage        string    `json:"accept_language"`
	DateAdded             time.Time `json:"date_added"`
	DateModified          time.Time `json:"date_modified"`
}

const (
	OrderStatusNew                = 1
	OrderStatusPending            = 2
	OrderStatusPrepareForShipping = 5
	OrderStatusPayed              = 17
)
