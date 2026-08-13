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

type drainingWorker interface {
	RunUntilStopped(context.Context, context.Context) error
}

type adminServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
	Close() error
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
	worker, err := notifications.NewWorker(store, sender, logger, notifications.WorkerConfig{WorkerID: cfg.WorkerID, BatchSize: cfg.BatchSize, LeaseDuration: cfg.LeaseDuration, MaxAttempts: cfg.MaxAttempts, BackoffBase: cfg.BackoffBase, BackoffMax: cfg.BackoffMax, PollInterval: cfg.PollInterval, Random: rand.Float64, Metrics: metricRegistry})
	if err != nil {
		return err
	}
	adminHandler, err := workeradmin.New(workeradmin.Config{Logger: logger, Readiness: func(ctx context.Context) error { return database.Ready(ctx, db) }, Metrics: metricRegistry})
	if err != nil {
		return err
	}
	adminServer := &http.Server{Addr: cfg.HTTPAddress, Handler: adminHandler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute, ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError)}
	return serveWorker(rootCtx, cfg.ShutdownTimeout, worker, adminServer)
}

func serveWorker(ctx context.Context, shutdownTimeout time.Duration, worker drainingWorker, admin adminServer) error {
	claimCtx, stopClaims := context.WithCancel(context.Background())
	defer stopClaims()
	attemptCtx, forceCancel := context.WithCancel(context.Background())
	defer forceCancel()
	workerResult := make(chan error, 1)
	adminResult := make(chan error, 1)
	go func() { workerResult <- worker.RunUntilStopped(claimCtx, attemptCtx) }()
	go func() { adminResult <- admin.ListenAndServe() }()

	workerDone, adminDone := false, false
	var result error
	select {
	case <-ctx.Done():
	case workerErr := <-workerResult:
		workerDone = true
		workerResult = nil
		if workerErr == nil {
			result = errors.New("notification worker stopped unexpectedly")
		} else {
			result = fmt.Errorf("notification worker: %w", workerErr)
		}
	case adminErr := <-adminResult:
		adminDone = true
		adminResult = nil
		if adminErr == nil || errors.Is(adminErr, http.ErrServerClosed) {
			result = errors.New("worker admin listener stopped unexpectedly")
		} else {
			result = fmt.Errorf("worker admin listener: %w", adminErr)
		}
	}
	stopClaims()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	var shutdownResult chan error
	shutdownDone := adminDone
	if !shutdownDone {
		shutdownResult = make(chan error, 1)
		go func() { shutdownResult <- admin.Shutdown(shutdownCtx) }()
	}
	deadline := shutdownCtx.Done()
	forced := false
	for !workerDone || !adminDone || !shutdownDone {
		select {
		case workerErr := <-workerResult:
			workerDone = true
			workerResult = nil
			if workerErr != nil {
				result = errors.Join(result, fmt.Errorf("notification worker: %w", workerErr))
			}
		case adminErr := <-adminResult:
			adminDone = true
			adminResult = nil
			if adminErr != nil && !errors.Is(adminErr, http.ErrServerClosed) {
				result = errors.Join(result, fmt.Errorf("worker admin listener: %w", adminErr))
			}
		case shutdownErr := <-shutdownResult:
			shutdownDone = true
			shutdownResult = nil
			if shutdownErr != nil && (!forced || !errors.Is(shutdownErr, context.DeadlineExceeded)) {
				result = errors.Join(result, fmt.Errorf("shut down worker admin listener: %w", shutdownErr))
				if closeErr := admin.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
					result = errors.Join(result, fmt.Errorf("force close worker admin listener: %w", closeErr))
				}
			}
		case <-deadline:
			forced = true
			deadline = nil
			result = errors.Join(result, fmt.Errorf("worker shutdown grace period expired: %w", context.DeadlineExceeded))
			forceCancel()
			if closeErr := admin.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
				result = errors.Join(result, fmt.Errorf("force close worker admin listener: %w", closeErr))
			}
		}
	}
	return result
}
