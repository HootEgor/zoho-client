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

// TestUAConfigIsReadyForPaymentsBeingTurnedOn resolves the UA shop's real config file with
// features.payments forced on, which is the state it will be deployed in once the Tranzzo flow
// lands. Doing it here rather than on the day of the switch catches the two failures that config
// can hide while payments are off: a zoho.payment_statuses map that validation would reject, and a
// Tranzzo state that resolves to an empty Zoho picklist value — which Zoho would accept as a
// Payments record with no Status at all.
func TestUAConfigIsReadyForPaymentsBeingTurnedOn(t *testing.T) {
	path := filepath.Join("..", "..", "zohoclient-config-ua.yml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("zohoclient-config-ua.yml not present")
	}

	c := &Config{}
	if err := readForTest(path, c); err != nil {
		t.Fatalf("read: %v", err)
	}

	on := true
	c.Site.Features.Payments = &on

	site, err := c.SiteSettings()
	if err != nil {
		t.Fatalf("SiteSettings() with payments on = %v", err)
	}
	if site.PaymentSource != PaymentSourceTranzzo {
		t.Fatalf("PaymentSource = %q, want %q", site.PaymentSource, PaymentSourceTranzzo)
	}

	// The module's whole vocabulary — PAY_STATE_* in the OpenCart extension.
	for _, state := range []string{"init", "auth", "capture", "void"} {
		if got := site.PaymentStatus(state); got == "" {
			t.Errorf("PaymentStatus(%q) = empty; every Tranzzo state needs a picklist value", state)
		}
	}
	// And anything outside it must be reported as an error, not silently dropped.
	if got := site.PaymentStatus("requires_capture"); got == "" {
		t.Error("an unknown status resolved to empty instead of the error picklist value")
	}
}

// TestUAStatusMapCoversTheTranzzoTriggers pins the two phrases the OpenCart Tranzzo module
// triggers on. The same strings travel in both directions: inbound as the webhook status this map
// must resolve, outbound as the status we put in the oc_tranzzo_queue payload.
//
// They are NOT the module's built-in defaults — the live Zoho picklist says "Опрацювання
// замовлення" and "Відмінено" where ZohoStub ships "Прийнятий, очікується оплата" and "Отмена
// заказа" — so the module's own capture/cancel admin lists have to carry these values instead.
// This test is the reminder that the two sides are coupled by nothing but agreement on a phrase.
func TestUAStatusMapCoversTheTranzzoTriggers(t *testing.T) {
	path := filepath.Join("..", "..", "zohoclient-config-ua.yml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("zohoclient-config-ua.yml not present")
	}

	c := &Config{}
	if err := readForTest(path, c); err != nil {
		t.Fatalf("read: %v", err)
	}
	site, err := c.SiteSettings()
	if err != nil {
		t.Fatalf("SiteSettings() = %v", err)
	}

	for _, tt := range []struct {
		status string
		want   int
	}{
		{"Опрацювання замовлення", 2},
		{"Відмінено", site.StatusCanceled},
	} {
		if got := site.OrderStatusIdByName(tt.status); got != tt.want {
			t.Errorf("OrderStatusIdByName(%q) = %d, want %d", tt.status, got, tt.want)
		}
	}

	// Every polled status must be resolvable, or an order this shop syncs cannot be moved back
	// by a webhook carrying the status it was synced at.
	for _, id := range site.PollStatuses {
		name := site.OrderStatusName(id)
		if got := site.OrderStatusIdByName(name); got != id {
			t.Errorf("polled status %d resolves to %q, which maps back to %d", id, name, got)
		}
	}
}
