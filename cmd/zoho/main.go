package main

import (
	"flag"
	"log/slog"
	"time"
	"zohoclient/bot"
	"zohoclient/impl/core"
	"zohoclient/internal/config"
	"zohoclient/internal/database"
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
			// Set up Telegram handler for the logger
			lg = logger.SetupTelegramHandler(lg, tgBot, slog.LevelDebug)
			lg.With(
				slog.String("bot_name", conf.Telegram.BotName),
			).Info("telegram bot initialized")

			// Start the bot in a goroutine
			go func() {
				if err := tgBot.Start(); err != nil {
					lg.Error("telegram bot error", slog.String("error", err.Error()))
				}
			}()
		}
	}

	lg.Info("starting zohoclient", slog.String("config", *configPath), slog.String("env", conf.Env))
	lg.Debug("debug messages enabled")

	handler := core.New(lg)

	db, err := database.NewSQLClient(conf, lg)
	if err != nil {
		lg.With(
			sl.Err(err),
		).Error("mysql client")
	}
	if db != nil {
		handler.SetRepository(db)
		lg.With(
			slog.String("host", conf.SQL.HostName),
			slog.String("port", conf.SQL.Port),
			slog.String("user", conf.SQL.UserName),
			slog.String("database", conf.SQL.Database),
		).Info("mysql client initialized")
		defer db.Close()

		lg.Info("mysql stats", slog.String("connections", db.Stats()))
		go func() {
			ticker := time.NewTicker(30 * time.Minute)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					lg.Info("mysql", slog.String("stats", db.Stats()))
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
		lg.With(
			sl.Err(err),
		).Error("product repository")
	} else {
		handler.SetProductRepository(prodRepo)
		lg.With(
			slog.String("url", conf.ProdRepo.ProdUrl),
		).Info("product repository initialized")
	}

	if zoho != nil {
		handler.SetZoho(zoho)
		lg.Info("zoho service initialized")
	} else {
		lg.Error("zoho service not initialized")
	}

	handler.Start()

	//if conf.Telegram.Enabled {
	//	tg, e := telegram.New(conf.Telegram.ApiKey, lg)
	//	if e != nil {
	//		lg.Error("telegram api", sl.Err(e))
	//	}
	//	//if mongo != nil {
	//	//	tg.SetDatabase(mongo)
	//	//}
	//	tg.Start()
	//	lg.Info("telegram api initialized")
	//	handler.SetMessageService(tg)
	//}

	lg.Error("service stopped")
}
