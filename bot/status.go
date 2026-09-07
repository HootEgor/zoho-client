package bot

import (
	"fmt"
	"strings"
	"zohoclient/entity"

	tgbotapi "github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
)

// StatusProvider is the service snapshot the /status command renders. It stays nil when the bot
// runs without a core to ask, and the command then says so instead of failing.
type StatusProvider interface {
	Status() entity.ServiceStatus
}

// status handles /status — the same snapshot the authenticated /zoho/status endpoint serves,
// rendered for reading on a phone.
func (t *TgBot) status(b *tgbotapi.Bot, ctx *ext.Context) error {
	userId := ctx.EffectiveUser.Id
	if !t.isAdmin(userId) {
		_, err := ctx.EffectiveMessage.Reply(b, "You are not authorized to use this command.", nil)
		return err
	}

	if t.statusProvider == nil {
		t.plainResponse(userId, "Service status is not available\\.")
		return nil
	}

	t.plainResponse(userId, formatStatus(t.statusProvider.Status()))
	return nil
}

// formatStatus renders a service snapshot as a MarkdownV2 message.
func formatStatus(s entity.ServiceStatus) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("%s *Service: %s*\n", overallIcon(s.Status), Sanitize(s.Status)))
	sb.WriteString("\nSite: " + Sanitize(orDash(s.Site)))
	sb.WriteString("\nEnv: " + Sanitize(orDash(s.Env)))
	sb.WriteString("\nUptime: " + Sanitize(s.Uptime))
	if s.DryRun {
		// Worth shouting: in this mode nothing reaches Zoho, inbound webhooks are not applied
		// to OpenCart, and no order is marked synced.
		sb.WriteString("\n⚠️ DRY RUN — nothing is written to Zoho or OpenCart")
	}

	sb.WriteString("\n\n*Components*")
	for _, c := range s.Components {
		sb.WriteString(fmt.Sprintf("\n%s %s: %s", componentIcon(c.State), Sanitize(c.Name), Sanitize(c.State)))
		if c.Detail != "" {
			sb.WriteString(" — " + Sanitize(c.Detail))
		}
	}

	sb.WriteString("\n\n*Features*")
	for _, f := range []struct {
		name string
		on   bool
	}{
		{"payments", s.Features.Payments},
		{"customer sync", s.Features.CustomerSync},
		{"b2b", s.Features.B2B},
		{"smartsender", s.Features.SmartSender},
	} {
		sb.WriteString(fmt.Sprintf("\n%s %s", featureIcon(f.on), Sanitize(f.name)))
	}

	sb.WriteString("\n\n*Orders*")
	sb.WriteString("\nPoll interval: " + Sanitize(s.Orders.PollInterval))
	if s.Orders.LastRunAt == nil {
		sb.WriteString("\nLast poll: none yet")
	} else {
		sb.WriteString("\nLast poll: " + Sanitize(formatVersionTime(*s.Orders.LastRunAt)) +
			Sanitize(fmt.Sprintf(" (%d ms)", s.Orders.LastRunMs)))
		sb.WriteString(Sanitize(fmt.Sprintf("\nQueued %d · synced %d · failed %d",
			s.Orders.LastQueued, s.Orders.LastSynced, s.Orders.LastFailed)))
	}
	sb.WriteString(Sanitize(fmt.Sprintf("\nSince start: %d synced, %d failed",
		s.Orders.TotalSynced, s.Orders.TotalFailed)))

	if s.Orders.LastError != "" {
		sb.WriteString("\n\n*Last error*")
		if s.Orders.LastErrorAt != nil {
			sb.WriteString("\n" + Sanitize(formatVersionTime(*s.Orders.LastErrorAt)))
		}
		sb.WriteString("\n" + Sanitize(s.Orders.LastError))
	}

	return sb.String()
}

func overallIcon(status string) string {
	if status == entity.StatusOK {
		return "✅"
	}
	return "❌"
}

func componentIcon(state string) string {
	switch state {
	case entity.ComponentUp:
		return "✅"
	case entity.ComponentDown:
		return "❌"
	case entity.ComponentDisabled:
		return "➖"
	default:
		return "❔"
	}
}

func featureIcon(on bool) string {
	if on {
		return "✅"
	}
	return "➖"
}

func orDash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}
