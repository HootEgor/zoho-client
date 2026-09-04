package config

import "testing"

// TestPostType covers every (shipping_code, shipping_method) pair observed in the production
// OpenCart database of the first shop, plus the code-only fallback path.
func TestPostType(t *testing.T) {
	site := DefaultSiteSettings()

	tests := []struct {
		code   string
		method string
		want   string
	}{
		// InPost courier, all three locales
		{"filterit1.filterit0", "InPost Kurier", site.postTypes[PostKeyInPostCourier]},
		{"filterit1.filterit0", "InPost Кур'єр", site.postTypes[PostKeyInPostCourier]},
		{"filterit1.filterit0", "InPost Courier", site.postTypes[PostKeyInPostCourier]},
		// InPost parcel machine
		{"filterit1.filterit1", "InPost Paczkomat", site.postTypes[PostKeyInPostTerminal]},
		{"filterit1.filterit1", "InPost Поштомат", site.postTypes[PostKeyInPostTerminal]},
		{"filterit1.filterit1", "InPost Parcel machine", site.postTypes[PostKeyInPostTerminal]},
		// Bare InPost
		{"filterit0.filterit2", "InPost", site.postTypes[PostKeyInPost]},
		// DHL
		{"dhl_country.dhl_country", "DHL Courier", site.postTypes[PostKeyDHLCourier]},
		{"dhl_country.dhl_country", "DHL Kurier", site.postTypes[PostKeyDHLCourier]},
		{"dhl_country.dhl_country", "DHL Кур'єр", site.postTypes[PostKeyDHLCourier]},
		{"filterit2.filterit0", "DHL Kurier", site.postTypes[PostKeyDHLCourier]},
		{"filterit2.filterit1", "DHL Paczkomat", site.postTypes[PostKeyDHLCourier]},
		{"filterit0.filterit0", "DHL", site.postTypes[PostKeyDHLCourier]},
		// Name wins over a disagreeing code (real row: InPost courier code, DHL name)
		{"filterit1.filterit0", "DHL Kurier", site.postTypes[PostKeyDHLCourier]},
		// Pickup
		{"pickup.pickup", "Odbiór osobisty", site.postTypes[PostKeyPickup]},
		{"pickup.pickup", "Самовивіз із магазину", site.postTypes[PostKeyPickup]},
		{"pickup.pickup", "Pickup From Store", site.postTypes[PostKeyPickup]},
		// Unrecognised name falls back to the code
		{"dhl_country.dhl_country", "shipping", site.postTypes[PostKeyDHLCourier]},
		{"pickup.pickup", "", site.postTypes[PostKeyPickup]},
		// No picklist value exists for these
		{"filterit3.filterit0", "Worldwide delivery", ""},
		{"filterit3.filterit0", "Доставка за кордон", ""},
		{"filterit3.filterit1", "Wysyłka na cały świat", ""},
		{"fedex_country.fedex_country", "FEEDEX Courier", ""},
		{"fedex_country.fedex_country", "FEEDEX Кур'єр", ""},
		{"", "", ""},
	}

	for _, tt := range tests {
		if got := site.PostType(tt.code, tt.method); got != tt.want {
			t.Errorf("PostType(%q, %q) = %q, want %q", tt.code, tt.method, got, tt.want)
		}
	}
}
