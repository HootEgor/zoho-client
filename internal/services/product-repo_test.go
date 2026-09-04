package services

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetProductZohoID_URLShape pins the request the repository receives. It holds one Zoho
// product id per site, so sending the UA shop's lookups without ?site returns the Polish shop's
// ids — which Zoho then rejects with FILTER_CRITERIA_NOT_SATISFIED when the order is created.
func TestGetProductZohoID_URLShape(t *testing.T) {
	tests := []struct {
		name     string
		siteCode string
		wantPath string
		wantSite string
	}{
		{"no site parameter when no site code is set", "", "/bot/product/uid-1", ""},
		// A numeric-looking code must survive as the string it is.
		{"scoped by site code", "00005", "/bot/product/uid-1", "00005"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotSite string
			var siteSent bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotSite = r.URL.Query().Get("site")
				_, siteSent = r.URL.Query()["site"]
				_, _ = w.Write([]byte(`{"success":true,"data":{"id":"ZOHO1"}}`))
			}))
			defer srv.Close()

			repo := &ProductRepo{productUrl: srv.URL + "/bot/product", siteCode: tt.siteCode}

			id, err := repo.GetProductZohoID("uid-1")
			if err != nil {
				t.Fatalf("GetProductZohoID() error = %v", err)
			}
			if id != "ZOHO1" {
				t.Errorf("id = %q, want ZOHO1", id)
			}
			if gotPath != tt.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if tt.wantSite == "" && siteSent {
				t.Error("site parameter was sent though no site code is configured")
			}
			if gotSite != tt.wantSite {
				t.Errorf("site = %q, want %q", gotSite, tt.wantSite)
			}
		})
	}
}
