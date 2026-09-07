package request

import (
	"bytes"
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

// MaxBodySize caps how much of a request body is read into memory.
const MaxBodySize = 1 << 20 // 1 MiB

// MaxLoggedBody caps how much of a raw payload Snippet returns.
const MaxLoggedBody = 4096

// Decode decodes request body into Request struct
func Decode(r *http.Request) (*Request, error) {
	req, _, err := DecodeWithBody(r)
	return req, err
}

// DecodeWithBody decodes the request body into Request and also returns the raw
// bytes it read, so a handler can log the payload that failed to decode. The raw
// bytes are returned even on error - that is the point of it.
func DecodeWithBody(r *http.Request) (*Request, []byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodySize))
	if err != nil {
		return nil, body, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, body, ErrEmptyBody
	}
	// UseNumber keeps every number in Data as its literal text instead of a float64. Data is an
	// interface{} that DecodeArrayData re-marshals into the target struct, and a Zoho record id
	// sent as a bare JSON number (739178000064455061) is 18 digits - float64 carries 15 and would
	// hand the round-trip a different id (739178000064455000), which resolves to no order at all.
	var req Request
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		return nil, body, err
	}
	return &req, body, nil
}

// Snippet renders a raw body for logging, trimmed and truncated to
// MaxLoggedBody bytes.
func Snippet(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return ""
	}
	if len(trimmed) > MaxLoggedBody {
		return string(trimmed[:MaxLoggedBody]) + "...[truncated]"
	}
	return string(trimmed)
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
