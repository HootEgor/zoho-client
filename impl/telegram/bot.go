package telegram

import (
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"log/slog"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
)

type Repository interface {
	GetSubscriptions() ([]entity.Subscription, error)
	AddSubscription(subscription *entity.Subscription) error
	UpdateSubscription(subscription *entity.Subscription) error
	DeleteSubscription(subscription *entity.Subscription) error
}

// TgBot implements EventHandler
type TgBot struct {
	api           *tgbotapi.BotAPI
	database      Repository
	subscriptions map[int]entity.Subscription
	invites       []string
	event         chan MessageContent
	send          chan MessageContent
	log           *slog.Logger
}

type MessageContent struct {
	ChatID int64
	Text   string
}

func New(apiKey string, log *slog.Logger) (*TgBot, error) {
	tgBot := &TgBot{
		subscriptions: make(map[int]entity.Subscription),
		event:         make(chan MessageContent, 100),
		send:          make(chan MessageContent, 100),
		log:           log.With(sl.Module("telegram")),
	}
	api, err := tgbotapi.NewBotAPI(apiKey)
	if err != nil {
		return nil, err
	}
	tgBot.log.With(sl.Secret("api_key", apiKey)).Debug("telegram bot created")
	tgBot.api = api
	return tgBot, nil
}

// SetDatabase attach database service
func (b *TgBot) SetDatabase(database Repository) {
	b.database = database
}

func (b *TgBot) Start() {
	b.subscriptions = make(map[int]entity.Subscription)
	if b.database != nil {
		subscriptions, err := b.database.GetSubscriptions()
		if err != nil {
			b.log.Error("getting subscriptions", sl.Err(err))
		}
		if subscriptions != nil {
			for _, subscription := range subscriptions {
				b.subscriptions[subscription.UserID] = subscription
			}
		}
		b.log.With(slog.Int("count", len(b.subscriptions))).Info("subscriptions loaded")
	}
	go b.sendPump()
	go b.eventPump()
	go b.updatesPump()
}

// Start listening for updates
func (b *TgBot) updatesPump() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates, err := b.api.GetUpdatesChan(u)
	if err != nil {
		b.log.Error("getting updates", sl.Err(err))
		return
	}
	for update := range updates {
		if update.Message == nil {
			continue
		}
		if !update.Message.IsCommand() {
			if b.checkInviteCode(update.Message.Text) {
				b.send <- MessageContent{ChatID: update.Message.Chat.ID, Text: b.confirmSubscription(&update)}
			}
			continue
		}
		switch update.Message.Command() {
		case "start":
			b.send <- MessageContent{ChatID: update.Message.Chat.ID, Text: b.subscribe(&update)}
		case "invite":
			if b.isAdmin(&update) {
				code := generatePinCode()
				b.invites = append(b.invites, code)
				b.send <- MessageContent{ChatID: update.Message.Chat.ID, Text: code}
			}
		case "clear":
			if b.isAdmin(&update) {
				b.invites = []string{}
				b.send <- MessageContent{ChatID: update.Message.Chat.ID, Text: "Invite codes cleared"}
			}
		case "list":
			if b.isAdmin(&update) {
				msg := "Invite codes:\n"
				for _, code := range b.invites {
					msg += fmt.Sprintf("%v\n", code)
				}
				b.send <- MessageContent{ChatID: update.Message.Chat.ID, Text: msg}
			}
		case "stop":
			b.send <- MessageContent{ChatID: update.Message.Chat.ID, Text: b.deleteSubscription(&update)}
		case "test":
			msg := fmt.Sprintf("*%v*: `%v`\n %v", "MONITOR", "Warn", "This is a test notification, relax")
			b.send <- MessageContent{ChatID: update.Message.Chat.ID, Text: msg}
		default:
			b.send <- MessageContent{ChatID: update.Message.Chat.ID, Text: "Unknown command"}
		}
	}
}

// eventPump sending events to all subscribers
func (b *TgBot) eventPump() {
	for {
		if event, ok := <-b.event; ok {
			for _, subscription := range b.subscriptions {
				if subscription.IsActive() {
					b.sendMessage(int64(subscription.UserID), event.Text)
				}
			}
		}
	}
}

// sendPump sending messages to users
func (b *TgBot) sendPump() {
	for {
		if event, ok := <-b.send; ok {
			go b.sendMessage(event.ChatID, event.Text)
		}
	}
}

// sendMessage common routine to send a message via bot API
func (b *TgBot) sendMessage(id int64, text string) {
	msg := tgbotapi.NewMessage(id, text)
	msg.ParseMode = "MarkdownV2"
	_, err := b.api.Send(msg)
	if err != nil {
		b.log.Warn("sending message", sl.Err(err))
		safeMsg := tgbotapi.NewMessage(id, fmt.Sprintf("This message caused an error:\n%v", removeMarkup(text)))
		_, err = b.api.Send(safeMsg)
		if err != nil {
			b.log.Error("sending no markup message", sl.Err(err))
			// maybe error was while parsing, so we can send a message about this error
			msg = tgbotapi.NewMessage(id, fmt.Sprintf("Error: %v", err))
			_, err = b.api.Send(msg)
			if err != nil {
				b.log.Error("sending message", sl.Err(err))
			}
		}
	}
}

func (b *TgBot) SendEventMessage(em *entity.EventMessage) error {
	var msg string
	if em.Sender != nil {
		msg = fmt.Sprintf("*%v*: `%v`\n", em.Sender.Name, em.Subject)
	} else {
		msg = fmt.Sprintf("`%v`\n", em.Subject)
	}
	if em.Text != "" {
		msg += fmt.Sprintf("%v\n", sanitize(em.Text))
	}
	if em.Payload != nil {
		payload := fmt.Sprintf("%v\n", em.Payload)
		msg += fmt.Sprintf("```\n%v\n```", sanitize(payload))
	}
	b.event <- MessageContent{Text: msg}
	return nil
}
