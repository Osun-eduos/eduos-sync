// Command eduos-sync is the sync service for the Osun EduOS mobile apps.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Osun-eduos/eduos-sync/internal/apiclient"
	"github.com/Osun-eduos/eduos-sync/internal/auth"
	"github.com/Osun-eduos/eduos-sync/internal/config"
	"github.com/Osun-eduos/eduos-sync/internal/server"
	"github.com/Osun-eduos/eduos-sync/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	api := apiclient.New(cfg.APIURL)
	srv := &server.Server{
		Store: db, API: api, Refs: apiclient.NewReferenceCache(api), Verifier: auth.NewVerifier(cfg.JWTSecret),
		RegisterDays: cfg.RegisterDays, MaxEvents: cfg.MaxEvents, MaxBodyBytes: cfg.MaxBodyBytes, Now: time.Now, Log: log,
	}
	httpSrv := &http.Server{
		Addr: ":" + cfg.Port, Handler: srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 120 * time.Second,
	}
	go func() {
		log.Info("eduos-sync listening", "port", cfg.Port, "api", cfg.APIURL)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdown)
}
