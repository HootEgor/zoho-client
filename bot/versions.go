package bot

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"

	tgbotapi "github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
)

// maxVersionsListed caps a /versions reply. An order that has been edited in Zoho all day can
// accumulate more versions than fit in one Telegram message, and the recent ones are what an
// admin is looking at.
const maxVersionsListed = 30

// versionTimeFormat renders stored timestamps in the server's local time; MongoDB hands them back
// as UTC.
const versionTimeFormat = "2006-01-02 15:04:05"

// Usage strings are written pre-escaped for MarkdownV2: the angle brackets around a placeholder
// and the underscore inside it are reserved characters, and an unescaped one makes Telegram reject
// the whole message.
const (
	escapedOrderIDArg   = "<order\\_id\\>"
	escapedVersionIDArg = "<version\\_id\\>"
	usageVersions       = "Usage: /versions " + escapedOrderIDArg
	usageVersion        = "Usage: /version " + escapedOrderIDArg + " " + escapedVersionIDArg
)

// versionList handles /versions <order_id> — the directory of stored versions of one order.
func (t *TgBot) versionList(b *tgbotapi.Bot, ctx *ext.Context) error {
	userId := ctx.EffectiveUser.Id
	if !t.isAdmin(userId) {
		_, err := ctx.EffectiveMessage.Reply(b, "You are not authorized to use this command.", nil)
		return err
	}

	args := strings.Fields(ctx.EffectiveMessage.Text)
	if len(args) < 2 {
		t.plainResponse(userId, usageVersions)
		return nil
	}

	orderID, ok := t.parseOrderID(userId, args[1])
	if !ok {
		return nil
	}

	repo, ok := t.versionRepo(userId)
	if !ok {
		return nil
	}

	versions, err := repo.ListOrderVersions(orderID)
	if err != nil {
		t.log.With(slog.Int64("order_id", orderID), sl.Err(err)).Warn("listing order versions")
		t.plainResponse(userId, "Failed to read version history: "+Sanitize(err.Error()))
		return nil
	}
	if len(versions) == 0 {
		t.plainResponse(userId, fmt.Sprintf("No stored versions for order %d\\.", orderID))
		return nil
	}

	t.plainResponse(userId, formatVersionList(orderID, versions))
	return nil
}

// formatVersionList renders the version directory of one order as a MarkdownV2 message, keeping
// only the most recent maxVersionsListed entries.
func formatVersionList(orderID int64, versions []entity.Version) string {
	shown := versions
	if len(shown) > maxVersionsListed {
		shown = shown[len(shown)-maxVersionsListed:]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*Order %d* — %d stored version\\(s\\)", orderID, len(versions)))
	if len(shown) < len(versions) {
		sb.WriteString(fmt.Sprintf(", last %d shown", len(shown)))
	}
	sb.WriteString("\n")
	for _, v := range shown {
		sb.WriteString(fmt.Sprintf("\n%s  —  %s",
			Sanitize(v.ID), Sanitize(formatVersionTime(v.CreationDate))))
	}
	sb.WriteString(fmt.Sprintf("\n\nDetails: /version %d %s", orderID, escapedVersionIDArg))

	return sb.String()
}

// versionDetails handles /version <order_id> <version_id> — status, total and item count of one
// stored version.
func (t *TgBot) versionDetails(b *tgbotapi.Bot, ctx *ext.Context) error {
	userId := ctx.EffectiveUser.Id
	if !t.isAdmin(userId) {
		_, err := ctx.EffectiveMessage.Reply(b, "You are not authorized to use this command.", nil)
		return err
	}

	args := strings.Fields(ctx.EffectiveMessage.Text)
	if len(args) < 3 {
		t.plainResponse(userId, usageVersion+"\nList the versions of an order with "+usageVersions)
		return nil
	}

	orderID, ok := t.parseOrderID(userId, args[1])
	if !ok {
		return nil
	}
	versionID := args[2]

	repo, ok := t.versionRepo(userId)
	if !ok {
		return nil
	}

	version, err := repo.GetOrderVersion(orderID, versionID)
	if err != nil {
		t.log.With(
			slog.Int64("order_id", orderID),
			slog.String("version_id", versionID),
			sl.Err(err),
		).Warn("reading order version")
		t.plainResponse(userId, "Failed to read version: "+Sanitize(err.Error()))
		return nil
	}
	if version == nil {
		t.plainResponse(userId, fmt.Sprintf("Order %d has no version %s\\.", orderID, Sanitize(versionID)))
		return nil
	}

	summary, err := version.Summary()
	if err != nil {
		t.log.With(
			slog.Int64("order_id", orderID),
			slog.String("version_id", versionID),
			sl.Err(err),
		).Warn("decoding order version payload")
		t.plainResponse(userId, fmt.Sprintf("Version %s of order %d is stored, but its payload could not be decoded: %s",
			Sanitize(versionID), orderID, Sanitize(err.Error())))
		return nil
	}

	t.plainResponse(userId, formatVersionDetails(orderID, *version, summary))
	return nil
}

// formatVersionDetails renders one version as a MarkdownV2 message. Fields the payload does not
// carry are left out rather than shown empty.
func formatVersionDetails(orderID int64, version entity.Version, s entity.VersionSummary) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*Order %d · version %s*\n", orderID, Sanitize(version.ID)))
	sb.WriteString("\nSaved: " + Sanitize(formatVersionTime(version.CreationDate)))

	if src := versionSourceName(s.Source); src != "" {
		sb.WriteString("\nSource: " + Sanitize(src))
	}
	if s.Subject != "" {
		sb.WriteString("\nSubject: " + Sanitize(s.Subject))
	}
	if s.ZohoID != "" {
		sb.WriteString("\nZoho id: " + Sanitize(s.ZohoID))
	}

	status := s.Status
	if status == "" {
		status = "—"
	}
	sb.WriteString("\nStatus: " + Sanitize(status))

	if s.HasTotal {
		total := strconv.FormatFloat(s.GrandTotal, 'f', 2, 64)
		if s.Currency != "" {
			total += " " + s.Currency
		}
		sb.WriteString("\nTotal: " + Sanitize(total))
	} else {
		sb.WriteString("\nTotal: —")
	}

	sb.WriteString(fmt.Sprintf("\nProducts: %d", s.Products))
	if s.ShippingLines > 0 {
		sb.WriteString(fmt.Sprintf("\nShipping lines: %d", s.ShippingLines))
	}

	return sb.String()
}

// versionSourceName translates a version source into the wording an admin reads.
func versionSourceName(source string) string {
	switch source {
	case entity.VersionSourceZoho:
		return "pushed to Zoho"
	case entity.VersionSourceWebhook:
		return "received from Zoho"
	default:
		return ""
	}
}

func formatVersionTime(t time.Time) string {
	return t.Local().Format(versionTimeFormat)
}

// parseOrderID reports the parsed order id, replying to the admin and returning false when the
// argument is not a usable order id.
func (t *TgBot) parseOrderID(userId int64, arg string) (int64, bool) {
	orderID, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || orderID <= 0 {
		t.plainResponse(userId, fmt.Sprintf("Invalid order id: %s", Sanitize(arg)))
		return 0, false
	}
	return orderID, true
}

// versionRepo returns the version history repository, replying to the admin and returning false
// when the internal database is disabled.
func (t *TgBot) versionRepo(userId int64) (VersionRepository, bool) {
	if t.versions == nil {
		t.plainResponse(userId, "Order version history is not available: the internal database is disabled\\.")
		return nil, false
	}
	return t.versions, true
}
