package entity

type ApiOrder struct {
	ZohoID       int64            `json:"zoho_id" validation:"required"`
	Status       string           `json:"status" validation:"required"`
	GrandTotal   float64          `json:"grand_total" validation:"required"`
	OrderedItems []ApiOrderedItem `json:"ordered_items" validation:"required,dive"`
}

type ApiOrderedItem struct {
	ZohoID   int64   `json:"zoho_id" validation:"required"`
	Price    float64 `json:"price" validation:"required"`
	Quantity int     `json:"quantity" validation:"required"`
}
