package entity

type Contact struct {
	FirstName string `json:"First_Name"`
	LastName  string `json:"Last_Name"`
	Email     string `json:"Email,omitempty"`
	Field2    string `json:"field2"`
	Phone     string `json:"Phone"`
}
