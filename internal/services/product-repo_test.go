package services

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetProductZohoID_URLShape pins the two path forms. The repository holds one Zoho product id
// per site, so sending the UA shop's lookups unscoped returns the Polish shop's ids — which Zoho
// then rejects with FILTER_CRITERIA_NOT_SATISFIED when the order is created.
func TestGetProductZohoID_URLShape(t *testing.T) {
	tests := []struct {
		name     string
		siteCode string
		wantPath string
	}{
		{"unscoped when no site code is set", "", "/product/uid-1"},
		{"scoped by site code", "ua", "/product/ua/uid-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte(`{"success":true,"data":{"id":"ZOHO1"}}`))
			}))
			defer srv.Close()

			repo := &ProductRepo{productUrl: srv.URL + "/product", siteCode: tt.siteCode}

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
		})
	}
}
