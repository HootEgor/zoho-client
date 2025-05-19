package entity

type Product struct {
	Model    string  `json:"model"`
	ZohoId   string  `json:"zoho_id"`
	Quantity int     `json:"quantity"`
	Price    float32 `json:"price"`
}
