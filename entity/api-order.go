package entity

type ApiOrder struct {
	ZohoID       string           `json:"zoho_id"`
	Status       string           `json:"status"`
	GrandTotal   float64          `json:"grand_total"`
	OrderedItems []ApiOrderedItem `json:"ordered_items"`
}

type ApiOrderedItem struct {
	ZohoID   string  `json:"zoho_id"`
	Price    float64 `json:"price"`
	Quantity int     `json:"quantity"`
}
