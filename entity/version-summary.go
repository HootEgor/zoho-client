package entity

import "encoding/json"

// Version sources, as reported by VersionSummary.Source.
const (
	// VersionSourceZoho marks a version written when the order was pushed to Zoho.
	VersionSourceZoho = "zoho"
	// VersionSourceWebhook marks a version written when Zoho sent the order back to us.
	VersionSourceWebhook = "webhook"
)

// VersionSummary is the readable digest of a stored order version.
//
// Versions are written from two different payload shapes — ZohoOrder for what we push to Zoho,
// ApiOrder for what a Zoho webhook sends back — so every field is filled from whichever spelling
// the payload actually carries.
type VersionSummary struct {
	Source        string
	Subject       string
	ZohoID        string
	Status        string
	Currency      string
	GrandTotal    float64
	HasTotal      bool
	Products      int
	ShippingLines int
}

// versionPayload declares both spellings of every field it reads. Both tags are exact matches, so
// encoding/json fills each from its own key and never falls back to its case-insensitive rule.
type versionPayload struct {
	// ZohoOrder — the payload we push to Zoho.
	Status       string        `json:"Status"`
	GrandTotal   *float64      `json:"Grand_Total"`
	Currency     string        `json:"Currency"`
	Subject      string        `json:"Subject"`
	OrderedItems []payloadItem `json:"Ordered_Items"`

	// ApiOrder — the payload a Zoho webhook sends back.
	StatusIn       string        `json:"status"`
	GrandTotalIn   *float64      `json:"grand_total"`
	ZohoID         string        `json:"zoho_id"`
	OrderedItemsIn []payloadItem `json:"ordered_items"`
}

// payloadItem is the part of a line item the summary needs. Only the inbound shape flags its
// shipping line; in an outbound payload shipping is an ordinary item carrying the shipping
// product id, and is counted with the products.
type payloadItem struct {
	Shipping bool `json:"is_shipping"`
}

// Summary decodes the stored payload into its digest. The product count is what the payload holds:
// an outbound order longer than zoho.chunk_size carries only its first chunk, the rest having been
// appended to the Zoho record by a follow-up call.
func (v Version) Summary() (VersionSummary, error) {
	var p versionPayload
	if err := json.Unmarshal([]byte(v.Payload), &p); err != nil {
		return VersionSummary{}, err
	}

	s := VersionSummary{
		Subject:  p.Subject,
		ZohoID:   p.ZohoID,
		Currency: p.Currency,
	}

	switch {
	case p.GrandTotal != nil || p.Subject != "" || p.OrderedItems != nil:
		s.Source = VersionSourceZoho
	case p.GrandTotalIn != nil || p.ZohoID != "" || p.OrderedItemsIn != nil:
		s.Source = VersionSourceWebhook
	}

	if p.Status != "" {
		s.Status = p.Status
	} else {
		s.Status = p.StatusIn
	}

	if p.GrandTotal != nil {
		s.GrandTotal, s.HasTotal = *p.GrandTotal, true
	} else if p.GrandTotalIn != nil {
		s.GrandTotal, s.HasTotal = *p.GrandTotalIn, true
	}

	items := p.OrderedItems
	if items == nil {
		items = p.OrderedItemsIn
	}
	for _, item := range items {
		if item.Shipping {
			s.ShippingLines++
		} else {
			s.Products++
		}
	}

	return s, nil
}
