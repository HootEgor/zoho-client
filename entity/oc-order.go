package entity

type OCOrder struct {
	AcceptLanguage         string `json:"accept_language"`
	AddressLocker          string `json:"addressLocker"`
	AffiliateID            int    `json:"affiliate_id"`
	BuildPrice             string `json:"build_price"`
	BuildPricePrefix       string `json:"build_price_prefix"`
	BuildPriceYesNo        string `json:"build_price_yes_no"`
	CalculatedSumm         string `json:"calculated_summ"`
	Comment                string `json:"comment"`
	CommentManager         string `json:"comment_manager"`
	Commission             string `json:"commission"`
	CurrencyCode           string `json:"currency_code"`
	CurrencyID             int    `json:"currency_id"`
	CurrencyValue          string `json:"currency_value"`
	CustomField            string `json:"custom_field"` // consider using map[string]interface{} if parsing needed
	CustomerGroupID        int    `json:"customer_group_id"`
	CustomerID             int    `json:"customer_id"`
	DateAdded              string `json:"date_added"`
	DateModified           string `json:"date_modified"`
	DeliveryPrice          string `json:"delivery_price"`
	Email                  string `json:"email"`
	Fax                    string `json:"fax"`
	Firstname              string `json:"firstname"`
	ForwardedIP            string `json:"forwarded_ip"`
	InvoiceNo              int    `json:"invoice_no"`
	InvoicePrefix          string `json:"invoice_prefix"`
	IP                     string `json:"ip"`
	LanguageID             int    `json:"language_id"`
	Lastname               string `json:"lastname"`
	ManagerProcessOrders   string `json:"manager_process_orders"`
	MarketingID            int    `json:"marketing_id"`
	OrderID                int    `json:"order_id"`
	OrderStatusID          int    `json:"order_status_id"`
	ParcelLocker           string `json:"parcelLocker"`
	PaymentAddress1        string `json:"payment_address_1"`
	PaymentAddress2        string `json:"payment_address_2"`
	PaymentAddressFormat   string `json:"payment_address_format"`
	PaymentCity            string `json:"payment_city"`
	PaymentCode            string `json:"payment_code"`
	PaymentCompany         string `json:"payment_company"`
	PaymentCountry         string `json:"payment_country"`
	PaymentCountryID       int    `json:"payment_country_id"`
	PaymentCustomField     string `json:"payment_custom_field"` // could also be parsed into []interface{}
	PaymentFirstname       string `json:"payment_firstname"`
	PaymentLastname        string `json:"payment_lastname"`
	PaymentMethod          string `json:"payment_method"`
	PaymentPostcode        string `json:"payment_postcode"`
	PaymentZone            string `json:"payment_zone"`
	PaymentZoneID          int    `json:"payment_zone_id"`
	RiseProductPrice       string `json:"rise_product_price"`
	RiseProductPricePrefix string `json:"rise_product_price_prefix"`
	RiseProductYesNo       string `json:"rise_product_yes_no"`
	ShippingAddress1       string `json:"shipping_address_1"`
	ShippingAddress2       string `json:"shipping_address_2"`
	ShippingAddressFormat  string `json:"shipping_address_format"`
	ShippingCity           string `json:"shipping_city"`
	ShippingCode           string `json:"shipping_code"`
	ShippingCompany        string `json:"shipping_company"`
	ShippingCountry        string `json:"shipping_country"`
	ShippingCountryID      int    `json:"shipping_country_id"`
	ShippingCustomField    string `json:"shipping_custom_field"` // could also be parsed into []interface{}
	ShippingFirstname      string `json:"shipping_firstname"`
	ShippingLastname       string `json:"shipping_lastname"`
	ShippingMethod         string `json:"shipping_method"`
	ShippingPostcode       string `json:"shipping_postcode"`
	ShippingZone           string `json:"shipping_zone"`
	ShippingZoneID         int    `json:"shipping_zone_id"`
	StoreID                int    `json:"store_id"`
	StoreName              string `json:"store_name"`
	StoreURL               string `json:"store_url"`
	Telephone              string `json:"telephone"`
	TextTTN                string `json:"text_ttn"`
	Total                  string `json:"total"`
	Tracking               string `json:"tracking"`
	UserAgent              string `json:"user_agent"`
}

const (
	OrderStatusPending    = 0
	OrderStatusNew        = 1
	OrderStatusApproved   = 2
	OrderStatusProcessing = 3
	OrderStatusShipped    = 4
)
