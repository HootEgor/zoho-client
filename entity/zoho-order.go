package entity

type ZohoOrder struct {
	ContactName        ContactName     `json:"Contact_Name"`
	OrderedItems       []OrderedItem   `json:"Ordered_Items"`
	Discount           float64         `json:"Discount"`
	DiscountP          float64         `json:"DiscountP"`
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
	Location           string          `json:"Location_DR"`
	OrderSource        string          `json:"Order_Source"`
}

type ContactName struct {
	ID string `json:"id"`
}

type OrderedItem struct {
	Product   ZohoProduct `json:"Product_Name"`
	Quantity  int64       `json:"Quantity"`
	Discount  float64     `json:"Discount"`
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
	Discount    float64   `json:"Discount"`
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
