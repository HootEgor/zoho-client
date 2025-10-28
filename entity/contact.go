package entity

type Contact struct {
	FirstName string `json:"First_Name"`
	LastName  string `json:"Last_Name"`
	Email     string `json:"Email,omitempty"`
	City      string `json:"field2"`
	Country   string `json:"field"`
	Phone     string `json:"Phone"`
}
