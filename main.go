package main

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

func main() {
	// Configure logging
	log := log.Output(os.Stdout)
	if os.Getenv("LOG_CONSOLE") != "" {
		log = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})
	}
	log.Info().Msg("mineshspc.com backend starting...")

	// Setup configuration parsing
	viper.SetEnvPrefix("mineshspc")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// Config file was found but another error was produced
			log.Fatal().Err(err).Msg("couldn't read viper config")
		}
	}

	// Open the database
	viper.SetDefault("db", "./mineshspc.db")
	dbPath := viper.GetString("db")
	log.Info().Str("db_path", dbPath).Msg("opening database...")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open database")
	}

	// Make sure to exit cleanly
	c := make(chan os.Signal, 1)
	healthcheckCtx, healthcheckCancel := context.WithCancel(context.Background())
	signal.Notify(c,
		syscall.SIGABRT,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGTERM,
	)
	go func() {
		for range c { // when the process is killed
			log.Info().Msg("Cleaning up")
			healthcheckCancel()
			db.Close()
			os.Exit(0)
		}
	}()

	app := NewApplication(&log, db)

	// Healthcheck loop
	healthcheckTimer := time.NewTimer(time.Second)
	healthcheckURL := app.Config.HealthcheckURL
	go func(log *zerolog.Logger) {
		if healthcheckURL == "" {
			log.Warn().Msg("Healthcheck URL not set, skipping healthcheck")
			return
		}
		for {
			select {
			case <-healthcheckCtx.Done():
				return
			case <-healthcheckTimer.C:
				log.Info().Msg("Sending healthcheck ping")
				resp, err := http.Get(healthcheckURL)
				if err != nil {
					log.Error().Err(err).Msg("Failed to send healtheck ping")
				} else if resp.StatusCode < 200 || 300 <= resp.StatusCode {
					log.Error().Int("status", resp.StatusCode).Msg("non-200 status code from healthcheck ping")
				}
				healthcheckTimer.Reset(30 * time.Second)
			}
		}
	}(&log)

	app.Start()
}
