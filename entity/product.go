package entity

type Product struct {
	UID      string  `json:"product_uid"`
	ZohoId   string  `json:"zoho_id"`
	Quantity int     `json:"quantity"`
	Price    float32 `json:"price"`
}

type ProductResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Id          string `json:"id"`
		Description string `json:"description"`
		Sku         string `json:"sku"`
	} `json:"data"`
	Message string `json:"message"`
}
