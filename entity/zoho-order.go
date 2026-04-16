package entity

// ZohoOrder represents a Sales Order record in Zoho CRM (Sales_Orders module).
// JSON field names map to Zoho CRM Sales_Orders module API names.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/modules/sales-orders.html
type ZohoOrder struct {
	ContactName        ContactName     `json:"Contact_Name"`
	OrderedItems       []OrderedItem   `json:"Ordered_Items"`
	Discount           float64         `json:"GetDiscount"`
	DiscountP          float64         `json:"DiscountP"`
	CouponTitle        string          `json:"Promocode"`
	CouponValue        float64         `json:"Promocode_discount"`
	Description        string          `json:"Description"`
	CustomerNo         string          `json:"Customer_No"`
	ShippingState      string          `json:"Shipping_State"`
	Tax                float64         `json:"Tax"`
	VAT                float64         `json:"VAT"`
	GrandTotal         float64         `json:"Grand_Total"`
	SubTotal           float64         `json:"Sub_Total"`
	Currency           string          `json:"Currency"`
	BillingCountry     string          `json:"Billing_Country"`
	Carrier            string          `json:"Carrier"`
	Status             string          `json:"Status"`
	SalesCommission    float64         `json:"Sales_Commission"`
	DueDate            string          `json:"Due_Date"`
	BillingStreet      string          `json:"Billing_Street"`
	Adjustment         float64         `json:"Adjustment"`
	TermsAndConditions string          `json:"Terms_and_Conditions"`
	BillingCode        string          `json:"Billing_Code"`
	ProductDetails     []ProductDetail `json:"Product_Details,omitempty"`
	Subject            string          `json:"Subject"`
	IDsite             string          `json:"ID_site"`
	NIP                string          `json:"NIP,omitempty"`
	Location           string          `json:"Location_DR"`
	OrderSource        string          `json:"Order_Source"`
	Postcode           string          `json:"postcode,omitempty"`
	RecipientCountry   string          `json:"A68fdec5b7ce138314daea92f2d691979,omitempty"`
	RecipientRegion    string          `json:"A937d270ccec10931cb2e573c485513f8,omitempty"`
	RecipientCity      string          `json:"Ac41409d106628a2bb742c9ac4214318f,omitempty"`
	RecipientAddress   string          `json:"A0d3aa57fb7d0fc67725ca891b3965663,omitempty"`
	RecipientCityId    string          `json:"A4ec4d0d585096ba020b4400761a90d5f,omitempty"`
	PostTerminal       string          `json:"A6994cbefd0422b84c177176fa76fd602,omitempty"`
}

type ContactName struct {
	ID string `json:"id"`
}

type OrderedItem struct {
	Product   ZohoProduct `json:"Product_Name"`
	Quantity  int64       `json:"Quantity"`
	Discount  float64     `json:"GetDiscount"`
	DiscountP float64     `json:"DiscountP"`
	ListPrice float64     `json:"List_Price"`
	Total     float64     `json:"Total"`
}

type ZohoProduct struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type ProductDetail struct {
	Product     ProductID `json:"product"`
	Quantity    int       `json:"quantity"`
	Discount    float64   `json:"GetDiscount"`
	ProductDesc string    `json:"product_description"`
	UnitPrice   float64   `json:"Unit Price"`
	LineTax     []LineTax `json:"line_tax"`
}

type ProductID struct {
	ID string `json:"id"`
}

type LineTax struct {
	Percentage float64 `json:"percentage"`
	Name       string  `json:"name"`
}
