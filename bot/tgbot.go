package bot

import (
	"fmt"
	"log"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"zohoclient/internal/lib/sl"

	tgbotapi "github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
)

type TgBot struct {
	log         *slog.Logger
	api         *tgbotapi.Bot
	botUsername string
	adminIds    []int64
	minLogLevel slog.Level
	adminLevels map[int64]slog.Level
}

func NewTgBot(botName, apiKey string, adminIdsStr string, log *slog.Logger) (*TgBot, error) {
	var adminIds []int64
	if adminIdsStr != "" {
		idStrs := strings.Split(adminIdsStr, ",")
		for _, idStr := range idStrs {
			id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid admin_id value: %q, must be a comma-separated list of integers", adminIdsStr)
			}
			adminIds = append(adminIds, id)
		}
	}

	// Default to warn level if not specified
	minLogLevel := slog.LevelDebug

	// Initialize admin levels map with default level for each admin
	adminLevels := make(map[int64]slog.Level)
	for i, adminId := range adminIds { // This loop will not run if adminIds is empty
		if i == 0 {
			adminLevels[adminId] = slog.LevelDebug
		} else {
			adminLevels[adminId] = slog.LevelWarn
		}
	}

	tgBot := &TgBot{
		log:         log.With(sl.Module("tgbot")),
		adminIds:    adminIds,
		botUsername: botName,
		minLogLevel: minLogLevel,
		adminLevels: adminLevels,
	}

	api, err := tgbotapi.NewBot(apiKey, nil)
	if err != nil {
		return nil, fmt.Errorf("creating api instance: %v", err)
	}
	tgBot.api = api

	return tgBot, nil
}

func (t *TgBot) Start() error {

	dispatcher := ext.NewDispatcher(&ext.DispatcherOpts{
		// If an error is returned by a handler, log it and continue going.
		Error: func(b *tgbotapi.Bot, ctx *ext.Context, err error) ext.DispatcherAction {
			log.Println("an error occurred while handling update:", err.Error())
			return ext.DispatcherActionNoop
		},
		MaxRoutines: ext.DefaultMaxRoutines,
	})
	updater := ext.NewUpdater(dispatcher, nil)

	dispatcher.AddHandler(handlers.NewCommand("level", t.level))

	// Start receiving updates.
	err := updater.StartPolling(t.api, &ext.PollingOpts{
		DropPendingUpdates: true,
		GetUpdatesOpts: &tgbotapi.GetUpdatesOpts{
			Timeout: 9,
			RequestOpts: &tgbotapi.RequestOpts{
				Timeout: time.Second * 10,
			},
		},
	})
	if err != nil {
		panic("failed to start polling: " + err.Error())
	}

	// Idle, to keep updates coming in, and avoid bot stopping.
	updater.Idle()

	// Set up an update configuration
	return nil
}

// SetMinLogLevel sets the minimum log level for all admin notifications
func (t *TgBot) SetMinLogLevel(level slog.Level) {
	t.minLogLevel = level

	// Update log level for all admins
	for _, adminId := range t.adminIds {
		t.adminLevels[adminId] = level
	}
}

// SetAdminLogLevel sets the minimum log level for a specific admin
func (t *TgBot) SetAdminLogLevel(adminId int64, level slog.Level) {
	t.adminLevels[adminId] = level
}

// level handles the /level command to set the minimum log level for admin notifications
func (t *TgBot) level(b *tgbotapi.Bot, ctx *ext.Context) error {
	// Get the user ID
	userId := ctx.EffectiveUser.Id

	// Check if the user is an admin
	isAdmin := false
	for _, adminId := range t.adminIds {
		if userId == adminId {
			isAdmin = true
			break
		}
	}

	if !isAdmin {
		_, err := ctx.EffectiveMessage.Reply(b, "You are not authorized to use this command.", nil)
		return err
	}

	// Get the level argument
	args := strings.Fields(ctx.EffectiveMessage.Text)
	if len(args) < 2 {
		currentLevel := t.adminLevels[userId]
		t.plainResponse(userId, fmt.Sprintf("Your current log level: %s\nAvailable levels: debug, info, warn, error", currentLevel.String()))
		return nil
	}

	// Parse the level
	levelStr := strings.ToLower(args[1])
	var level slog.Level
	switch levelStr {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		t.plainResponse(userId, fmt.Sprintf("Invalid level: %s\nAvailable levels: debug, info, warn, error", levelStr))
		return nil
	}

	// Set the level for this specific admin
	t.SetAdminLogLevel(userId, level)
	t.plainResponse(userId, fmt.Sprintf("Your log level set to: %s", level.String()))
	return nil
}

func (t *TgBot) SendMessage(msg string) {
	// Send message to all admins (using default log level)
	t.SendMessageWithLevel(msg, t.minLogLevel)
}

// SendMessageWithLevel sends a message to all admins with the specified log level
func (t *TgBot) SendMessageWithLevel(msg string, level slog.Level) {
	// Send message to all admins who have a log level that allows this message
	for _, adminId := range t.adminIds {
		// Check if this admin's log level allows this message
		adminLevel, exists := t.adminLevels[adminId]
		if !exists {
			// If admin doesn't have a specific level, use the default
			adminLevel = t.minLogLevel
		}

		// Only send if the message level is >= the admin's minimum level
		if level >= adminLevel {
			t.plainResponse(adminId, msg)
		}
	}
}

func (t *TgBot) plainResponse(chatId int64, text string) {

	//text = strings.ReplaceAll(text, "**", "*")
	//text = strings.ReplaceAll(text, "![", "[")
	//
	//sanitized := Sanitize(text)

	if text != "" {
		_, err := t.api.SendMessage(chatId, text, &tgbotapi.SendMessageOpts{
			ParseMode: "MarkdownV2",
		})
		if err != nil {
			t.log.With(
				slog.Int64("id", chatId),
			).Warn("sending message", sl.Err(err))
			_, _ = t.api.SendMessage(chatId, err.Error(), &tgbotapi.SendMessageOpts{})
			_, err = t.api.SendMessage(chatId, text, &tgbotapi.SendMessageOpts{})
			if err != nil {
				t.log.With(
					slog.Int64("id", chatId),
				).Error("sending safe message", sl.Err(err))
			}
		}
	} else {
		t.log.With(
			slog.Int64("id", chatId),
		).Debug("empty message")
	}
}
func Sanitize(input string) string {
	// Define a list of reserved characters that need to be escaped
	reservedChars := "\\_{}#+-.!|()[]=*"

	// Loop through each character in the input string
	sanitized := ""
	for _, char := range input {
		// Check if the character is reserved
		if strings.ContainsRune(reservedChars, char) {
			// Escape the character with a backslash
			sanitized += "\\" + string(char)
		} else {
			// Add the character to the sanitized string
			sanitized += string(char)
		}
	}

	return sanitized
}
