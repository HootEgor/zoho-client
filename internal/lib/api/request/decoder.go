package request

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// DecodeArrayData decodes request data into a typed array
// Handles both single objects and arrays - if a single object is provided, it wraps it in an array
// Usage: var users []entity.User; err := DecodeArrayData(req, &users)
func DecodeArrayData[T any](req *Request, target *[]T) error {
	if req.Data == nil {
		*target = []T{} // Empty array
		return nil
	}

	dataBytes, err := json.Marshal(req.Data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	// Try to unmarshal as array first
	err = json.Unmarshal(dataBytes, target)
	if err == nil {
		return nil // Successfully decoded as array
	}

	// If that failed, try to unmarshal as a single object and wrap it in an array
	var singleItem T
	err = json.Unmarshal(dataBytes, &singleItem)
	if err != nil {
		return fmt.Errorf("failed to unmarshal data: %w", err)
	}

	*target = []T{singleItem}
	return nil
}

// Binder is an interface for entities that can validate themselves
type Binder interface {
	Bind(*http.Request) error
}

// DecodeAndValidateArrayData decodes request data into a typed array and validates each item
// Works with types that have pointer receivers for Bind() method
// Usage: var users []entity.User; err := DecodeAndValidateArrayData(req, r, &users)
func DecodeAndValidateArrayData[T any](req *Request, httpReq *http.Request, target *[]T) error {
	// First decode the data
	err := DecodeArrayData(req, target)
	if err != nil {
		return err
	}

	// Then validate each item - take address to work with pointer receivers
	for i := range *target {
		item := &(*target)[i]

		// Type assert to Binder interface
		if binder, ok := any(item).(Binder); ok {
			if err := binder.Bind(httpReq); err != nil {
				return fmt.Errorf("validation failed for item at index %d: %w", i, err)
			}
		}
	}

	return nil
}

func DecodeAndValidateData[T any](req *Request, httpReq *http.Request, target *T) error {
	return DecodeAndValidateArrayData(req, httpReq, &[]T{*target})
}
