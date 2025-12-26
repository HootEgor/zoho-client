package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"zohoclient/bot"
	"zohoclient/impl/core"
	"zohoclient/internal/config"
	"zohoclient/internal/database"
	"zohoclient/internal/http-server/api"
	"zohoclient/internal/lib/logger"
	"zohoclient/internal/lib/sl"
	"zohoclient/internal/services"
)

func main() {
	configPath := flag.String("conf", "config.yml", "path to config file")
	logPath := flag.String("log", "/var/log/", "path to log file directory")
	flag.Parse()

	conf := config.MustLoad(*configPath)
	lg := logger.SetupLogger(conf.Env, *logPath)

	// Initialize Telegram bot if enabled
	var tgBot *bot.TgBot
	if conf.Telegram.Enabled {
		var err error
		tgBot, err = bot.NewTgBot(conf.Telegram.BotName, conf.Telegram.ApiKey, conf.Telegram.AdminId, lg)
		if err != nil {
			lg.Error("failed to initialize telegram bot", slog.String("error", err.Error()))
		} else {
			lg = logger.SetupTelegramHandler(lg, tgBot, slog.LevelDebug)
			lg.With(
				slog.String("bot", conf.Telegram.BotName),
			).Info("telegram bot initialized")

			go func() {
				if err := tgBot.Start(); err != nil {
					lg.Error("telegram bot error", slog.String("error", err.Error()))
				}
			}()
		}
	}

	lg.Info("starting zohoclient", slog.String("config", *configPath), slog.String("env", conf.Env))
	lg.Debug("debug messages enabled")

	handler := core.New(lg, *conf)

	db, err := database.NewSQLClient(conf, lg)
	if err != nil {
		lg.With(sl.Err(err)).Error("mysql client")
	}
	if db != nil {
		handler.SetRepository(db)
		lg.With(
			slog.String("host", conf.SQL.HostName),
			slog.String("port", conf.SQL.Port),
			slog.String("user", conf.SQL.UserName),
			slog.String("database", conf.SQL.Database),
		).Info("mysql client initialized")

		lg.Debug("mysql stats", slog.String("connections", db.Stats()))
		go func() {
			ticker := time.NewTicker(6 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					lg.Debug("mysql", slog.String("stats", db.Stats()))
				}
			}
		}()
	}

	zoho, err := services.NewZohoService(conf, lg)
	if err != nil {
		lg.Error("zoho service", sl.Err(err))
	}

	prodRepo, err := services.NewProductRepo(conf, lg)
	if err != nil {
		lg.With(sl.Err(err)).Error("product repository")
	} else {
		handler.SetProductRepository(prodRepo)
		lg.With(
			slog.String("url", conf.ProdRepo.ProdUrl),
		).Info("product repository initialized")
	}

	if zoho != nil {
		handler.SetZoho(zoho)
	} else {
		lg.Error("zoho service not initialized")
	}

	handler.SetAuthKey(conf.Listen.ApiKey)
	handler.Start()

	// Create HTTP server
	server, err := api.New(conf, lg, handler)
	if err != nil {
		lg.Error("server create", sl.Err(err))
		return
	}

	// Channel to listen for shutdown signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server in goroutine
	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			lg.Error("server start", sl.Err(err))
		}
	}()

	// Wait for shutdown signal
	sig := <-quit
	lg.Info("shutdown signal received", slog.String("signal", sig.String()))

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Graceful shutdown sequence
	lg.Info("shutting down services...")

	// 1. Stop accepting new HTTP requests
	if err := server.Shutdown(ctx); err != nil {
		lg.Error("http server shutdown", sl.Err(err))
	}

	// 2. Stop order processing
	handler.Stop()

	// 3. Stop Telegram bot
	if tgBot != nil {
		tgBot.Stop()
	}

	// 4. Close database connection
	if db != nil {
		db.Close()
	}

	lg.Info("service stopped gracefully")
}
