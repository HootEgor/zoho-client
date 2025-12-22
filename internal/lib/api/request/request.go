package request

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type Request struct {
	Data       interface{} `json:"data,omitempty"`
	Method     string      `json:"method"`
	FullUpdate bool        `json:"full_update"`
	Count      int         `json:"count"`
	Page       int         `json:"page"`
	Total      int         `json:"total"`
}

// Common errors
var (
	ErrEmptyBody = errors.New("request body is empty")
)

// Decode decodes request body into Request struct
func Decode(r *http.Request) (*Request, error) {
	var req Request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		if err == io.EOF {
			return nil, ErrEmptyBody
		}
		return nil, err
	}
	return &req, nil
}

// UnmarshalData unmarshals the Data field into a typed value
func (r *Request) UnmarshalData(target interface{}) error {
	if r.Data == nil {
		return errors.New("data field is nil")
	}

	dataBytes, err := json.Marshal(r.Data)
	if err != nil {
		return err
	}
	return json.Unmarshal(dataBytes, target)
}

// GetPagination returns offset and limit based on page and count
// offset = (page - 1) * count
func (r *Request) GetPagination() (offset, limit int) {
	if r.Count <= 0 {
		r.Count = 100 // Default items per page
	}
	if r.Page <= 0 {
		r.Page = 1 // Default to first page
	}
	offset = (r.Page - 1) * r.Count
	limit = r.Count
	return offset, limit
}
