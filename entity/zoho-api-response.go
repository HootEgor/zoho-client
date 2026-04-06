package entity

import "encoding/json"

// TokenResponse is the OAuth 2.0 token response from Zoho Accounts.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/refresh.html
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	Scope       string `json:"scope"`
	ApiDomain   string `json:"api_domain"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// ZohoAPIResponse is the standard envelope for Zoho CRM v8 API responses.
// Each item in Data corresponds to one record in the request.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/insert-records.html
type ZohoAPIResponse struct {
	Data []ZohoResponseItem `json:"data"`
}

type ZohoResponseItem struct {
	Status         string          `json:"status,omitempty"`
	Message        string          `json:"message,omitempty"`
	Code           string          `json:"code,omitempty"`
	Details        json.RawMessage `json:"details,omitempty"`
	Action         string          `json:"action,omitempty"`          // "create" or "update" for upsert
	DuplicateField string          `json:"duplicate_field,omitempty"` // field used for duplicate check
}

type MultipleErrors struct {
	Errors []Error `json:"errors"`
}

type Error struct {
	Status  string           `json:"status,omitempty"`
	Message string           `json:"message,omitempty"`
	Code    string           `json:"code,omitempty"`
	Details DuplicateDetails `json:"details,omitempty"`
}

type DuplicateDetails struct {
	APIName         string          `json:"api_name"`
	JSONPath        string          `json:"json_path"`
	MoreRecords     bool            `json:"more_records"`
	DuplicateRecord DuplicateRecord `json:"duplicate_record"`
}

type DuplicateRecord struct {
	ID     string     `json:"id"`
	Owner  ZohoUser   `json:"Owner"`
	Module ZohoModule `json:"module"`
}

type ZohoUser struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	ZUID string `json:"zuid"`
}

type ZohoModule struct {
	APIName string `json:"api_name"`
	ID      string `json:"id"`
}

type SuccessContactDetails struct {
	ID        string       `json:"id"`
	CreatedBy ZohoUserInfo `json:"Created_By"`
}

type ZohoUserInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ErrorDetails struct {
	APIName  string `json:"api_name"`
	JSONPath string `json:"json_path"`
}

type SuccessOrderDetails struct {
	ID           string       `json:"id"`
	CreatedBy    ZohoUserInfo `json:"Created_By"`
	CreatedTime  string       `json:"Created_Time"`
	ModifiedBy   ZohoUserInfo `json:"Modified_By"`
	ModifiedTime string       `json:"Modified_Time"`
	Approval     string       `json:"$approval_state"`
}
