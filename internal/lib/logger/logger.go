package logger

import (
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"zohoclient/bot"
)

const (
	envLocal = "local"
	envDev   = "dev"
	envProd  = "prod"
	// defaultLogFileName is used when the config names no file. Each instance needs its own:
	// two processes appending to one file interleave their output and share its rotation.
	defaultLogFileName = "zohoclient.log"
)

// SetupLogger opens path/fileName for append and returns a logger writing to it. An empty fileName
// falls back to defaultLogFileName. In the "local" environment nothing is opened and logs go to
// stdout instead.
func SetupLogger(env, path, fileName string) *slog.Logger {
	var logger *slog.Logger
	var logFile *os.File
	var err error

	if env != envLocal {
		logPath := logFilePath(path, fileName)
		logFile, err = os.OpenFile(logPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			log.Fatal("error opening log file: ", err)
		}
		log.Printf("env: %s; log file: %s", env, logPath)
	}

	switch env {
	case envLocal:
		logger = slog.New(
			slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}),
		)
	case envDev:
		logger = slog.New(
			slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelDebug}),
		)
	case envProd:
		logger = slog.New(
			slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}),
		)
	default:
		log.Fatal("invalid environment: ", env)
	}

	return logger
}

func logFilePath(path, fileName string) string {
	if fileName == "" {
		fileName = defaultLogFileName
	}
	return filepath.Join(path, fileName)
}

// SetupTelegramHandler adds a Telegram handler to the logger
func SetupTelegramHandler(logger *slog.Logger, tgBot *bot.TgBot, minLevel slog.Level) *slog.Logger {
	if tgBot == nil {
		return logger
	}

	// Get the existing handler from the logger
	existingHandler := logger.Handler()

	// Create a new Telegram handler that wraps the existing handler
	tgHandler := NewTelegramHandler(existingHandler, tgBot, minLevel)

	// Create a new logger with the Telegram handler
	return slog.New(tgHandler)
}
