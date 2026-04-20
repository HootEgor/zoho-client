package entity

type Contact struct {
	FirstName        string `json:"First_Name,omitempty"`
	LastName         string `json:"Last_Name,omitempty"`
	Email            string `json:"Email,omitempty"`
	City             string `json:"field2,omitempty"`
	Country          string `json:"field,omitempty"`
	Phone            string `json:"Phone,omitempty"`
	CustomerCategory string `json:"customer_category,omitempty"`
}
