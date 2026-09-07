package entity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"zohoclient/internal/lib/validate"
)

type ApiOrder struct {
	ZohoID       string           `json:"zoho_id" validate:"required"`
	Status       string           `json:"status" validate:"required"`
	GrandTotal   float64          `json:"grand_total" validate:"gt=0"`
	Coupon       string           `json:"coupon"`
	OrderedItems []ApiOrderedItem `json:"ordered_items" validate:"required,dive"`
	// ModifiedTime is Zoho's Sales_Orders.Modified_Time for the version that
	// triggered this webhook (RFC3339). Used to suppress echo webhooks caused
	// by our own writes — see impl/core/api-order.go.
	ModifiedTime string `json:"modified_time"`
}

type ApiOrderedItem struct {
	ZohoID   string  `json:"zoho_id" validate:"required"`
	Price    float64 `json:"price" validate:"gt=0"`
	Total    float64 `json:"total" validate:"gt=0"`
	Quantity int     `json:"quantity" validate:"gt=0"`
	Shipping bool    `json:"is_shipping"`
}

// UnmarshalJSON reads zoho_id through zohoID so a numeric id is accepted, and leaves every other
// field to the standard decoder.
func (o *ApiOrder) UnmarshalJSON(data []byte) error {
	type alias ApiOrder
	aux := struct {
		ZohoID json.RawMessage `json:"zoho_id"`
		*alias
	}{alias: (*alias)(o)}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	id, err := zohoID(aux.ZohoID)
	if err != nil {
		return fmt.Errorf("order zoho_id: %w", err)
	}
	o.ZohoID = id
	return nil
}

func (i *ApiOrderedItem) UnmarshalJSON(data []byte) error {
	type alias ApiOrderedItem
	aux := struct {
		ZohoID json.RawMessage `json:"zoho_id"`
		*alias
	}{alias: (*alias)(i)}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	id, err := zohoID(aux.ZohoID)
	if err != nil {
		return fmt.Errorf("item zoho_id: %w", err)
	}
	i.ZohoID = id
	return nil
}

// zohoID reads a Zoho record id given either as a JSON string ("739178000064455061") or as a bare
// JSON number (739178000064455061). Zoho's Deluge functions produce both - the UA site sends the
// Sales Order id unquoted while its subform ids are quoted - and an id is an opaque identifier
// either way, so both become the same string.
//
// The digits come from the literal, never through float64: a Zoho id is 18 digits and float64
// carries 15, so decoding it as a number would silently change the id.
func zohoID(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return "", nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	var n json.Number
	if err := json.Unmarshal(trimmed, &n); err != nil {
		return "", fmt.Errorf("must be a string or a number, got %s", trimmed)
	}
	return n.String(), nil
}

func (o *ApiOrder) Bind(_ *http.Request) error {
	return validate.Struct(o)
}
