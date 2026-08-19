package core

import "testing"

// TestMapPostType covers every (shipping_code, shipping_method) pair observed in the
// production OpenCart database, plus the code-only fallback path.
func TestMapPostType(t *testing.T) {
	tests := []struct {
		code   string
		method string
		want   string
	}{
		// InPost courier, all three locales
		{"filterit1.filterit0", "InPost Kurier", postTypeInPostCourier},
		{"filterit1.filterit0", "InPost Кур'єр", postTypeInPostCourier},
		{"filterit1.filterit0", "InPost Courier", postTypeInPostCourier},
		// InPost parcel machine
		{"filterit1.filterit1", "InPost Paczkomat", postTypeInPostTerminal},
		{"filterit1.filterit1", "InPost Поштомат", postTypeInPostTerminal},
		{"filterit1.filterit1", "InPost Parcel machine", postTypeInPostTerminal},
		// Bare InPost
		{"filterit0.filterit2", "InPost", postTypeInPost},
		// DHL
		{"dhl_country.dhl_country", "DHL Courier", postTypeDHLCourier},
		{"dhl_country.dhl_country", "DHL Kurier", postTypeDHLCourier},
		{"dhl_country.dhl_country", "DHL Кур'єр", postTypeDHLCourier},
		{"filterit2.filterit0", "DHL Kurier", postTypeDHLCourier},
		{"filterit2.filterit1", "DHL Paczkomat", postTypeDHLCourier},
		{"filterit0.filterit0", "DHL", postTypeDHLCourier},
		// Name wins over a disagreeing code (real row: InPost courier code, DHL name)
		{"filterit1.filterit0", "DHL Kurier", postTypeDHLCourier},
		// Pickup
		{"pickup.pickup", "Odbiór osobisty", postTypePickup},
		{"pickup.pickup", "Самовивіз із магазину", postTypePickup},
		{"pickup.pickup", "Pickup From Store", postTypePickup},
		// Unrecognised name falls back to the code
		{"dhl_country.dhl_country", "shipping", postTypeDHLCourier},
		{"pickup.pickup", "", postTypePickup},
		// No picklist value exists for these
		{"filterit3.filterit0", "Worldwide delivery", ""},
		{"filterit3.filterit0", "Доставка за кордон", ""},
		{"filterit3.filterit1", "Wysyłka na cały świat", ""},
		{"fedex_country.fedex_country", "FEEDEX Courier", ""},
		{"fedex_country.fedex_country", "FEEDEX Кур'єр", ""},
		{"", "", ""},
	}

	for _, tt := range tests {
		if got := mapPostType(tt.code, tt.method); got != tt.want {
			t.Errorf("mapPostType(%q, %q) = %q, want %q", tt.code, tt.method, got, tt.want)
		}
	}
}
