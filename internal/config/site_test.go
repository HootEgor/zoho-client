package config

import (
	"strings"
	"testing"
	"time"
	"zohoclient/entity"
)

// TestSiteSettings_EmptyConfigMatchesDefaults is the safety net for the first shop: a config file
// with no site: or zoho: value sections must resolve to exactly the behaviour the service had when
// all of this was hardcoded.
func TestSiteSettings_EmptyConfigMatchesDefaults(t *testing.T) {
	got, err := (&Config{}).SiteSettings()
	if err != nil {
		t.Fatalf("SiteSettings() error = %v", err)
	}
	want := DefaultSiteSettings()

	if got.Location.String() != "Europe/Warsaw" {
		t.Errorf("Location = %q, want Europe/Warsaw", got.Location)
	}
	if got.LanguageID != 2 || got.LookbackDays != 30 || got.BatchLimit != 10 {
		t.Errorf("language/lookback/limit = %d/%d/%d, want 2/30/10", got.LanguageID, got.LookbackDays, got.BatchLimit)
	}
	if got.PollInterval != 2*time.Minute || got.CustomerPollInterval != 5*time.Minute {
		t.Errorf("intervals = %v/%v, want 2m/5m", got.PollInterval, got.CustomerPollInterval)
	}
	if got.NipCustomFieldID != "2" || got.PostTerminalField != "field29" {
		t.Errorf("nip/terminal field = %q/%q", got.NipCustomFieldID, got.PostTerminalField)
	}
	if got.ShippingItemUID != "cd3cc23c-6dfb-11ec-b75f-00155d018000" {
		t.Errorf("ShippingItemUID = %q", got.ShippingItemUID)
	}
	if got.ChunkSize != 200 {
		t.Errorf("ChunkSize = %d, want 200", got.ChunkSize)
	}
	if !got.Payments || !got.CustomerSync || !got.B2B {
		t.Errorf("features = %t/%t/%t, want all true", got.Payments, got.CustomerSync, got.B2B)
	}
	if got.ZohoLocation != "Польша" || got.ZohoOrderSource != "OpenCart" {
		t.Errorf("location/source = %q/%q", got.ZohoLocation, got.ZohoOrderSource)
	}
	if got.OrderStatusName(1) != "Нове" {
		t.Errorf("OrderStatusName(1) = %q, want Нове", got.OrderStatusName(1))
	}
	if got.PaymentStatus("requires_capture") != "Кошти зарезервовано" {
		t.Errorf("PaymentStatus(requires_capture) = %q", got.PaymentStatus("requires_capture"))
	}
	if len(got.PollStatuses) != len(want.PollStatuses) {
		t.Errorf("PollStatuses = %v, want %v", got.PollStatuses, want.PollStatuses)
	}
	// The B2B group list moved out of entity.ClientDetails; group 7 was removed from it there.
	for _, id := range []int64{6, 16, 18, 19} {
		if !got.IsB2B(id) {
			t.Errorf("IsB2B(%d) = false, want true", id)
		}
	}
	for _, id := range []int64{0, 5, 7, 20} {
		if got.IsB2B(id) {
			t.Errorf("IsB2B(%d) = true, want false", id)
		}
	}
}

// TestSiteSettings_Overrides checks that a second shop's file actually displaces the defaults
// rather than merging oddly with them.
func TestSiteSettings_Overrides(t *testing.T) {
	c := &Config{}
	c.Site.Name = "shop-b"
	c.Site.TimeZone = "Europe/Kyiv"
	c.Site.LanguageID = 1
	c.Site.LookbackDays = 7
	c.Site.BatchLimit = 25
	c.Site.PollInterval = 300
	c.Site.Currencies = []string{"UAH"}
	c.Site.OrderStatuses.Poll = []int{1, 3}
	c.Site.B2BGroupIDs = []int64{}
	c.Site.TotalCodes = map[string]string{TotalKeyCoupon: "voucher"}
	off := false
	c.Site.Features.Payments = &off
	c.Site.Features.CustomerSync = &off
	c.Zoho.Location = "Україна"
	c.Zoho.OrderStatusMap = map[int]string{1: "Новий", 3: "Зібрано"}

	s, err := c.SiteSettings()
	if err != nil {
		t.Fatalf("SiteSettings() error = %v", err)
	}

	if s.Location.String() != "Europe/Kyiv" || s.LanguageID != 1 || s.BatchLimit != 25 {
		t.Errorf("overrides not applied: %s / %d / %d", s.Location, s.LanguageID, s.BatchLimit)
	}
	if s.PollInterval != 5*time.Minute {
		t.Errorf("PollInterval = %v, want 5m (seconds are converted)", s.PollInterval)
	}
	if s.AllowedCurrency("PLN") || !s.AllowedCurrency("UAH") {
		t.Error("Currencies replaced the defaults rather than merging with them")
	}
	// An explicitly empty list means "no group is B2B", not "use the defaults".
	if s.IsB2B(6) {
		t.Error("IsB2B(6) = true, want false for an empty b2b_group_ids")
	}
	// total_codes merges: the renamed code applies, the untouched ones keep their defaults.
	if s.TotalCode(TotalKeyCoupon) != "voucher" {
		t.Errorf("TotalCode(coupon) = %q, want voucher", s.TotalCode(TotalKeyCoupon))
	}
	if s.TotalCode(TotalKeyTax) != "tax" {
		t.Errorf("TotalCode(tax) = %q, want tax", s.TotalCode(TotalKeyTax))
	}
	if s.Payments || s.CustomerSync {
		t.Error("features not switched off")
	}
	// B2B is left unset, so it keeps its default of on.
	if !s.B2B {
		t.Error("B2B feature should default to on when the key is absent")
	}
	if s.OrderStatusName(3) != "Зібрано" {
		t.Errorf("OrderStatusName(3) = %q, want Зібрано", s.OrderStatusName(3))
	}
	if s.OrderStatusIdByName("Новий") != 1 {
		t.Errorf("OrderStatusIdByName(Новий) = %d, want 1", s.OrderStatusIdByName("Новий"))
	}
}

// TestSiteSettings_UnmappedStatusFallsBackToNew covers the polled statuses a shop has no Zoho name
// for (the payment-link states): they must still carry the new-order status, not an empty string.
func TestSiteSettings_UnmappedStatusFallsBackToNew(t *testing.T) {
	s := DefaultSiteSettings()
	for _, statusId := range []int{2, 22, 23, 999} {
		if got := s.OrderStatusName(statusId); got != "Нове" {
			t.Errorf("OrderStatusName(%d) = %q, want Нове", statusId, got)
		}
	}
	// The reverse lookup stays exact — an unknown Zoho status must not silently become an order id.
	if got := s.OrderStatusIdByName("Не існує"); got != -1 {
		t.Errorf("OrderStatusIdByName(unknown) = %d, want -1", got)
	}
}

func TestSiteSettings_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "shipping code mapped to an unknown post type",
			mutate: func(c *Config) {
				c.Site.ShippingCodeMap = map[string]string{"x.y": "nonexistent_key"}
			},
			wantErr: "not a key of zoho.post_types",
		},
		{
			name: "post types missing a key the keyword rules can produce",
			mutate: func(c *Config) {
				c.Site.ShippingCodeMap = map[string]string{}
				c.Zoho.PostTypes = map[string]string{PostKeyInPost: "InPost"}
			},
			wantErr: "zoho.post_types is missing key",
		},
		{
			name: "payment statuses incomplete while payments are on",
			mutate: func(c *Config) {
				c.Zoho.PaymentStatuses = map[string]string{entity.PaymentKeyPaid: "Оплачено"}
			},
			wantErr: "zoho.payment_statuses is missing key",
		},
		{
			name: "status map has no entry for the new-order status",
			mutate: func(c *Config) {
				c.Zoho.OrderStatusMap = map[int]string{5: "Збір"}
			},
			wantErr: "order_status_map has no entry",
		},
		{
			name: "unknown timezone",
			mutate: func(c *Config) {
				c.Site.TimeZone = "Mars/Olympus"
			},
			wantErr: "site.timezone",
		},
		{
			name: "unknown total code key",
			mutate: func(c *Config) {
				c.Site.TotalCodes = map[string]string{"handling": "handling"}
			},
			wantErr: "unknown key",
		},
		{
			name: "empty poll status list",
			mutate: func(c *Config) {
				c.Site.OrderStatuses.Poll = []int{}
				c.Site.BatchLimit = -1
			},
			wantErr: "batch_limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{}
			tt.mutate(c)
			_, err := c.SiteSettings()
			if err == nil {
				t.Fatalf("SiteSettings() = nil error, want one mentioning %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestSiteSettings_PaymentStatusesUncheckedWhenPaymentsOff: a site without wfsync never writes a
// Zoho Payments record, so it must not be forced to supply that picklist.
func TestSiteSettings_PaymentStatusesUncheckedWhenPaymentsOff(t *testing.T) {
	c := &Config{}
	off := false
	c.Site.Features.Payments = &off
	c.Zoho.PaymentStatuses = map[string]string{}

	if _, err := c.SiteSettings(); err != nil {
		t.Fatalf("SiteSettings() error = %v, want nil", err)
	}
}

// TestNewOrderStatusName_IsNotDerivedFromOrderStatus pins the asymmetry between the two
// directions: the Sales Order the sync writes always carries the new-order status, whatever the
// OpenCart status happens to be, while the reverse lookup still resolves every mapped name.
// Deriving the forward status from the OpenCart one would let a re-push overwrite a status a Zoho
// user had already moved on.
func TestNewOrderStatusName_IsNotDerivedFromOrderStatus(t *testing.T) {
	s := DefaultSiteSettings()

	if got := s.NewOrderStatusName(); got != "Нове" {
		t.Errorf("NewOrderStatusName() = %q, want Нове", got)
	}
	// Reverse direction keeps the full map: a webhook carrying any mapped name resolves.
	for name, want := range map[string]int{
		"Нове":              1,
		"Перевірка та збір": 5,
		"Оплачено, формування ТТН": 17,
	} {
		if got := s.OrderStatusIdByName(name); got != want {
			t.Errorf("OrderStatusIdByName(%q) = %d, want %d", name, got, want)
		}
	}
}
