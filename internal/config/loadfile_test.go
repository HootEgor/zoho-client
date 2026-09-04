package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfigYmlDecodes loads the repository's own config templates through cleanenv, which is the
// only way to prove the YAML shapes (int-keyed maps, nested feature booleans, lists) actually
// decode into the Config struct rather than silently landing on zero values.
func TestConfigYmlDecodes(t *testing.T) {
	for _, name := range []string{"config.yml", "zohoclient-config.yml", "zohoclient-config-ua.yml"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", name)
			if _, err := os.Stat(path); err != nil {
				t.Skipf("%s not present", name)
			}
			// MustLoad memoises via sync.Once, so decode directly here.
			c := &Config{}
			if err := readForTest(path, c); err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			site, err := c.SiteSettings()
			if err != nil {
				t.Fatalf("%s: SiteSettings() error = %v", name, err)
			}
			t.Log(site.LogValue())
			if site.Name == "" {
				t.Error("site.name did not decode")
			}
			if len(site.PollStatuses) == 0 {
				t.Error("site.order_statuses.poll did not decode")
			}
			if site.CustomerCategory(14) == "" && site.CustomerSync {
				t.Error("site.customer_categories did not decode (int-keyed map)")
			}
			// Keyword path, so this holds for any shop regardless of its shipping module codes.
			if site.PostType("", "InPost Paczkomat") == "" {
				t.Error("zoho.post_types did not decode")
			}
			if site.OrderStatusName(1) == "" {
				t.Error("zoho.order_status_map did not decode (int-keyed map)")
			}
			if site.Payments && site.PaymentStatus("requires_capture") == "" {
				t.Error("zoho.payment_statuses did not decode")
			}
		})
	}
}
