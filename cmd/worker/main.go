package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eidrogo/work-management-api/internal/config"
	"github.com/eidrogo/work-management-api/internal/metrics"
	"github.com/eidrogo/work-management-api/internal/notifications"
	"github.com/eidrogo/work-management-api/internal/platform/clock"
	"github.com/eidrogo/work-management-api/internal/platform/database"
	"github.com/eidrogo/work-management-api/internal/platform/email"
	"github.com/eidrogo/work-management-api/internal/platform/logging"
	"github.com/eidrogo/work-management-api/internal/workeradmin"
)

func main() {
	if err := run(); err != nil {
		log.Printf("worker terminated: %v", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.LoadWorker(nil)
	if err != nil {
		return fmt.Errorf("load worker configuration: %w", err)
	}
	logger, err := logging.New(cfg.LogLevel, os.Stdout)
	if err != nil {
		return err
	}
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(rootCtx, cfg.Database.URL, "work-management-worker", cfg.Database.MaxConns)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	store, err := notifications.NewStore(db)
	if err != nil {
		return err
	}
	sender, err := email.NewSMTP(email.Config{Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, Username: cfg.SMTP.Username, Password: cfg.SMTP.Password, From: cfg.SMTP.From, FromName: cfg.SMTP.FromName, TLSMode: email.TLSMode(cfg.SMTP.TLSMode), TLSServerName: cfg.SMTP.TLSServerName, TLSInsecureSkipVerify: cfg.SMTP.TLSInsecureSkipVerify, Timeout: cfg.SMTP.Timeout})
	if err != nil {
		return err
	}
	metricRegistry := metrics.New(store, time.Now)
	worker, err := notifications.NewWorker(store, sender, clock.System{}, logger, notifications.WorkerConfig{WorkerID: cfg.WorkerID, BatchSize: cfg.BatchSize, LeaseDuration: cfg.LeaseDuration, MaxAttempts: cfg.MaxAttempts, BackoffBase: cfg.BackoffBase, BackoffMax: cfg.BackoffMax, PollInterval: cfg.PollInterval, Random: rand.Float64, Metrics: metricRegistry})
	if err != nil {
		return err
	}
	adminHandler, err := workeradmin.New(workeradmin.Config{Logger: logger, Readiness: func(ctx context.Context) error { return database.Ready(ctx, db) }, Metrics: metricRegistry})
	if err != nil {
		return err
	}
	adminServer := &http.Server{Addr: cfg.HTTPAddress, Handler: adminHandler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute, ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError)}
	runCtx, cancelRun := context.WithCancel(rootCtx)
	defer cancelRun()
	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- worker.Run(runCtx) }()
	go func() { errorsChannel <- adminServer.ListenAndServe() }()
	select {
	case <-rootCtx.Done():
	case runErr := <-errorsChannel:
		if runErr != nil && !errors.Is(runErr, http.ErrServerClosed) {
			cancelRun()
			return runErr
		}
	}
	cancelRun()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return adminServer.Shutdown(shutdownCtx)
}
