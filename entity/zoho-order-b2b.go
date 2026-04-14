package entity

// ZohoOrderB2B represents a Deal record in the Zoho CRM Deals module for B2B orders.
// Uses currency-specific total fields (Grand_Total_UAH, etc.) because B2B deals
// may be denominated in different currencies.
// JSON field names map to Zoho CRM Deals module API names.
type ZohoOrderB2B struct {
	ContactName ContactName `json:"Contact_Name"`
	Goods       []Good      `json:"Products"`
	DiscountP   float64     `json:"total_discount"`
	Description string      `json:"Description"`
	//CustomerNo  string      `json:"Customer_No"`
	//ShippingState      string          `json:"Shipping_State"`
	VAT            float64 `json:"VAT"`
	GrandTotalUAH  float64 `json:"Grand_Total_UAH,omitempty"`
	GrandTotalUSD  float64 `json:"Grand_Total_USD,omitempty"`
	GrandTotalEUR  float64 `json:"Grand_Total_EUR,omitempty"`
	GrandTotalPLN  float64 `json:"Grand_Total_PLN,omitempty"`
	SubTotalUAH    float64 `json:"Total_UAH,omitempty"`
	SubTotalUSD    float64 `json:"Total_USD,omitempty"`
	SubTotalEUR    float64 `json:"Total_EUR,omitempty"`
	SubTotalPLN    float64 `json:"Total_PLN,omitempty"`
	Currency       string  `json:"Currency"`
	BillingCountry string  `json:"Country"`
	Status         string  `json:"Stage"`
	Pipeline       string  `json:"Pipeline"`
	BillingStreet  string  `json:"delivery_street"`
	//TermsAndConditions string          `json:"Terms_and_Conditions"`
	//BillingCode    string          `json:"Billing_Code"`
	Subject     string `json:"Deal_Name"`
	NIP         string `json:"NIP,omitempty"`
	Location    string `json:"Location"`
	OrderSource string `json:"Order_Source"`
}

// Good represents a record in the Zoho CRM custom "Goods" module, linked to a Deal.
// Currency-specific price/total fields are used instead of a single amount field.
type Good struct {
	Product   ZohoProduct `json:"Product"`
	Deal      ZohoDeal    `json:"Deal"`
	Name      string      `json:"Name"`
	Quantity  int64       `json:"Goods_quantity"`
	DiscountP float64     `json:"Discount"`
	PriceUAH  float64     `json:"Good_price,omitempty"`
	PriceUSD  float64     `json:"Price_USD,omitempty"`
	PriceEUR  float64     `json:"Price_EUR,omitempty"`
	PricePLN  float64     `json:"Price_PLN,omitempty"`
	TotalUAH  float64     `json:"Total,omitempty"`
	TotalUSD  float64     `json:"Total_USD,omitempty"`
	TotalEUR  float64     `json:"Total_EUR,omitempty"`
	TotalPLN  float64     `json:"Total_PLN,omitempty"`
}

type ZohoDeal struct {
	ID string `json:"id"`
}
