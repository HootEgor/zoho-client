package bot

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"zohoclient/entity"
)

func TestFormatVersionDetails(t *testing.T) {
	saved := time.Date(2026, 9, 1, 12, 3, 44, 0, time.Local)
	version := entity.Version{ID: "2", CreationDate: saved}
	summary := entity.VersionSummary{
		Source:        entity.VersionSourceWebhook,
		ZohoID:        "5000000123456",
		Status:        "Відправлено",
		GrandTotal:    1250.5,
		HasTotal:      true,
		Products:      2,
		ShippingLines: 1,
	}

	msg := formatVersionDetails(4210, version, summary)

	for _, want := range []string{
		"*Order 4210 · version 2*",
		"Saved: 2026\\-09\\-01 12:03:44",
		"Source: received from Zoho",
		"Zoho id: 5000000123456",
		"Status: Відправлено",
		"Total: 1250\\.50",
		"Products: 2",
		"Shipping lines: 1",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q\ngot:\n%s", want, msg)
		}
	}
}

func TestFormatVersionDetails_OutboundWithCurrency(t *testing.T) {
	version := entity.Version{ID: "0", CreationDate: time.Now()}
	summary := entity.VersionSummary{
		Source:     entity.VersionSourceZoho,
		Subject:    "Order 4210",
		Status:     "Нове замовлення",
		Currency:   "PLN",
		GrandTotal: 929.7,
		HasTotal:   true,
		Products:   3,
	}

	msg := formatVersionDetails(4210, version, summary)

	if !strings.Contains(msg, "Total: 929\\.70 PLN") {
		t.Errorf("expected a currency-suffixed total\ngot:\n%s", msg)
	}
	if !strings.Contains(msg, "Source: pushed to Zoho") {
		t.Errorf("expected the outbound source wording\ngot:\n%s", msg)
	}
	// Nothing was shipped separately in an outbound payload, so the line must be absent rather
	// than reported as zero.
	if strings.Contains(msg, "Shipping lines") {
		t.Errorf("shipping line should be omitted when there is none\ngot:\n%s", msg)
	}
	if strings.Contains(msg, "Zoho id") {
		t.Errorf("zoho id should be omitted when the payload has none\ngot:\n%s", msg)
	}
}

func TestFormatVersionDetails_MissingFields(t *testing.T) {
	msg := formatVersionDetails(7, entity.Version{ID: "0"}, entity.VersionSummary{})

	if !strings.Contains(msg, "Status: —") {
		t.Errorf("expected a placeholder status\ngot:\n%s", msg)
	}
	if !strings.Contains(msg, "Total: —") {
		t.Errorf("expected a placeholder total\ngot:\n%s", msg)
	}
	if !strings.Contains(msg, "Products: 0") {
		t.Errorf("expected a zero product count\ngot:\n%s", msg)
	}
	if strings.Contains(msg, "Source:") {
		t.Errorf("source should be omitted when unknown\ngot:\n%s", msg)
	}
}

func TestVersionSourceName(t *testing.T) {
	tests := map[string]string{
		entity.VersionSourceZoho:    "pushed to Zoho",
		entity.VersionSourceWebhook: "received from Zoho",
		"":                          "",
		"nonsense":                  "",
	}
	for source, want := range tests {
		if got := versionSourceName(source); got != want {
			t.Errorf("versionSourceName(%q) = %q, want %q", source, got, want)
		}
	}
}

func TestTgBotIsAdmin(t *testing.T) {
	bot := &TgBot{adminIds: []int64{10, 20}}
	if !bot.isAdmin(20) {
		t.Error("admin 20 should be recognised")
	}
	if bot.isAdmin(30) {
		t.Error("non-admin 30 should be rejected")
	}
	if (&TgBot{}).isAdmin(10) {
		t.Error("a bot with no admins should recognise nobody")
	}
}

// unescapedReserved returns the MarkdownV2 reserved characters a message leaves unescaped, minus
// the ones the caller uses deliberately as formatting. Telegram rejects a message that carries any
// of them raw, and plainResponse then has to fall back to sending it unformatted.
func unescapedReserved(msg, allowed string) []string {
	const reserved = "\\_{}#+-.!|()[]=*~`>"

	var bad []string
	runes := []rune(msg)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' {
			i++ // whatever follows a backslash is escaped
			continue
		}
		if strings.ContainsRune(reserved, runes[i]) && !strings.ContainsRune(allowed, runes[i]) {
			bad = append(bad, string(runes[i]))
		}
	}
	return bad
}

// TestUnescapedReserved guards the guard: a validator that never reports anything would let every
// escaping test below pass silently.
func TestUnescapedReserved(t *testing.T) {
	if bad := unescapedReserved("file.txt", ""); len(bad) != 1 || bad[0] != "." {
		t.Errorf("unescapedReserved(\"file.txt\") = %v, want [.]", bad)
	}
	if bad := unescapedReserved("file\\.txt", ""); len(bad) != 0 {
		t.Errorf("an escaped dot should not be reported, got %v", bad)
	}
	if bad := unescapedReserved("*bold*", "*"); len(bad) != 0 {
		t.Errorf("an allowed character should not be reported, got %v", bad)
	}
	if bad := unescapedReserved("a(b)c", ""); len(bad) != 2 {
		t.Errorf("both parentheses should be reported, got %v", bad)
	}
}

func TestVersionMessagesAreEscaped(t *testing.T) {
	saved := time.Date(2026, 9, 1, 12, 3, 44, 0, time.Local)

	// The bold markers are the only formatting these messages use.
	const boldOnly = "*"

	tests := []struct {
		name    string
		msg     string
		allowed string
	}{
		{"usage /versions", usageVersions, ""},
		{"usage /version", usageVersion, ""},
		{
			name:    "version list",
			msg:     formatVersionList(4210, []entity.Version{{ID: "0", CreationDate: saved}, {ID: "1", CreationDate: saved}}),
			allowed: boldOnly,
		},
		{
			name: "version details",
			msg: formatVersionDetails(4210,
				entity.Version{ID: "2", CreationDate: saved},
				entity.VersionSummary{
					Source:        entity.VersionSourceZoho,
					Subject:       "Order 4210 (dark-591B9EAB)",
					Status:        "Нове замовлення",
					Currency:      "PLN",
					GrandTotal:    929.7,
					HasTotal:      true,
					Products:      3,
					ShippingLines: 1,
				}),
			allowed: boldOnly,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if bad := unescapedReserved(tt.msg, tt.allowed); len(bad) > 0 {
				t.Errorf("unescaped MarkdownV2 characters %v in:\n%s", bad, tt.msg)
			}
		})
	}
}

func TestFormatVersionList_CapsAtMaxListed(t *testing.T) {
	versions := make([]entity.Version, maxVersionsListed+5)
	for i := range versions {
		versions[i] = entity.Version{ID: strconv.Itoa(i), CreationDate: time.Now()}
	}

	msg := formatVersionList(77, versions)

	if !strings.Contains(msg, strconv.Itoa(len(versions))+" stored version") {
		t.Errorf("the full count should still be reported\ngot:\n%s", msg)
	}
	if !strings.Contains(msg, "last 30 shown") {
		t.Errorf("expected a note that the list was trimmed\ngot:\n%s", msg)
	}
	// The oldest versions are dropped, the newest are kept.
	if strings.Contains(msg, "\n0  —") {
		t.Errorf("version 0 should have been trimmed\ngot:\n%s", msg)
	}
	if !strings.Contains(msg, "\n"+strconv.Itoa(len(versions)-1)+"  —") {
		t.Errorf("the newest version should be listed\ngot:\n%s", msg)
	}
}
