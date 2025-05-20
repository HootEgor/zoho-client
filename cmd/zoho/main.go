package main

import (
	"flag"
	"log/slog"
	"time"
	"zohoapi/impl/core"
	"zohoapi/internal/config"
	"zohoapi/internal/database"
	"zohoapi/internal/lib/logger"
	"zohoapi/internal/lib/sl"
	"zohoapi/internal/services"
)

func main() {

	configPath := flag.String("conf", "config.yml", "path to config file")
	logPath := flag.String("log", "/var/log/", "path to log file directory")
	flag.Parse()

	conf := config.MustLoad(*configPath)
	lg := logger.SetupLogger(conf.Env, *logPath)

	lg.Info("starting zohoapi", slog.String("config", *configPath), slog.String("env", conf.Env))
	lg.Debug("debug messages enabled")

	handler := core.New(lg)

	db, err := database.NewSQLClient(conf, lg)
	if err != nil {
		lg.Error("mysql client", sl.Err(err))
	}
	if db != nil {
		handler.SetRepository(db)
		lg.Info("mysql client initialized",
			slog.String("host", conf.SQL.HostName),
			slog.String("port", conf.SQL.Port),
			slog.String("user", conf.SQL.UserName),
			slog.String("database", conf.SQL.Database),
		)
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
