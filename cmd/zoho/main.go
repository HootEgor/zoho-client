package main

import (
	"context"
	"errors"
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
	repository "zohoclient/internal/database/mongo"
	"zohoclient/internal/database/sql"
	"zohoclient/internal/http-server/api"
	"zohoclient/internal/lib/logger"
	"zohoclient/internal/lib/sl"
	"zohoclient/internal/services"
)

func main() {
	configPath := flag.String("conf", "config.yml", "path to config file")
	logPath := flag.String("log", "/var/log/", "path to log file directory")
	backfillDay := flag.String("backfill", "", "repair the per-line discount of orders placed on this day (YYYY-MM-DD) and exit; reports only unless -apply is given")
	backfillApply := flag.Bool("apply", false, "with -backfill or -mark-synced: actually write the changes")
	markSynced := flag.Bool("mark-synced", false, "stamp every order that has no zoho_id with the [SKIP] sentinel so a freshly seeded shop does not push its history to Zoho, then exit; reports only unless -apply is given")
	flag.Parse()

	conf := config.MustLoad(*configPath)
	lg := logger.SetupLogger(conf.Env, *logPath, conf.Site.LogFile)

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
		}
	}

	// Resolve the site description before anything is constructed: a mistyped picklist key or an
	// unknown timezone must stop the process here, not surface on the first order that syncs.
	site, err := conf.SiteSettings()
	if err != nil {
		lg.With(sl.Err(err)).Error("invalid site configuration")
		os.Exit(1)
	}
	lg = lg.With(slog.String("site", site.Name))

	lg.Info("starting zohoclient", slog.String("config", *configPath), slog.String("env", conf.Env))
	lg.Info("site settings resolved", slog.String("settings", site.LogValue()))
	lg.Debug("debug messages enabled")

	handler := core.New(lg, *conf, site)

	db, err := sql.NewSQLClient(conf, site, lg)
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
			ticker := time.NewTicker(1 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					stats := db.Stats()
					if stats != "" {
						lg.Info("mysql", slog.String("stats", stats))
					}
				}
			}
		}()
	}

	zoho, err := services.NewZohoService(conf, site, lg)
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

	mongoClient, err := repository.NewMongoClient(conf, lg)
	if err != nil {
		lg.Error("failed to create mongo client", sl.Err(err))
	}
	if mongoClient != nil {
		handler.SetMongoRepository(mongoClient)
	}

	// Polling starts only here: the bot's version commands read the internal database, so it must
	// be handed over before any command handler can run. Notifications do not need the updater,
	// so everything logged above still reached the admins.
	if tgBot != nil {
		if mongoClient != nil {
			tgBot.SetVersionRepository(mongoClient)
		}
		go func() {
			if err := tgBot.Start(); err != nil {
				lg.Error("telegram bot error", slog.String("error", err.Error()))
			}
		}()
	}

	// Initialize SmartSender integration if enabled
	if conf.SmartSender.Enabled {
		smartSenderSvc, err := services.NewSmartSenderService(conf, lg)
		if err != nil {
			lg.Error("failed to create smartsender service", sl.Err(err))
		} else if smartSenderSvc != nil {
			handler.SetSmartSenderService(smartSenderSvc)
			lg.Info("SmartSender service initialized")
		}

		zohoFuncSvc, err := services.NewZohoFunctionsService(conf, lg)
		if err != nil {
			lg.Error("failed to create zoho functions service", sl.Err(err))
		} else if zohoFuncSvc != nil {
			handler.SetZohoFunctionsService(zohoFuncSvc)
			lg.Info("Zoho Functions service initialized")
		}

		handler.SetSmartSenderPollInterval(time.Duration(conf.SmartSender.PollInterval) * time.Second)
	}

	// One-shot maintenance mode: mark the history a freshly seeded shop starts with as already
	// handled, then exit. Run with the service stopped so the poller cannot pick orders up while
	// they are being stamped.
	if *markSynced {
		count, err := handler.MarkExistingOrdersSynced(*backfillApply)
		if err != nil {
			lg.With(sl.Err(err)).Error("mark synced failed")
			if db != nil {
				db.Close()
			}
			os.Exit(1)
		}
		if *backfillApply {
			lg.Info("orders marked as skipped", slog.Int64("count", count))
		} else {
			lg.Info("dry run: no rows written, re-run with -apply to mark them",
				slog.Int64("would_mark", count))
		}
		if db != nil {
			db.Close()
		}
		return
	}

	// One-shot maintenance mode: repair a day's orders and exit without starting the service,
	// so the poller cannot interleave with the rewrite.
	if *backfillDay != "" {
		day, err := time.ParseInLocation(time.DateOnly, *backfillDay, time.Local)
		if err != nil {
			lg.With(sl.Err(err)).Error("invalid -backfill date, want YYYY-MM-DD")
			os.Exit(1)
		}
		res, err := handler.BackfillOrderDiscounts(day, day.AddDate(0, 0, 1), *backfillApply)
		if err != nil {
			lg.With(sl.Err(err)).Error("backfill failed")
			if db != nil {
				db.Close()
			}
			os.Exit(1)
		}
		if !*backfillApply {
			lg.Info("dry run: nothing was written, re-run with -apply to correct these orders")
		}
		if db != nil {
			db.Close()
		}
		if res.Failed > 0 {
			os.Exit(1)
		}
		return
	}

	handler.SetAuthKey(conf.Listen.ApiKey)
	handler.Start()

	// Create an HTTP server
	server, err := api.New(conf, site, lg, handler)
	if err != nil {
		lg.Error("server create", sl.Err(err))
		return
	}

	// Channel to listen for shutdown signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server in goroutine
	go func() {
		if err := server.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			lg.Error("server start", sl.Err(err))
		}
	}()

	// Wait for a shutdown signal
	sig := <-quit
	lg.Info("shutdown signal received", slog.String("signal", sig.String()))

	// Create a shutdown context with timeout
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
