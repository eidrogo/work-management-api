package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/config"
	"github.com/eidrogo/work-management-api/internal/httpapi"
	"github.com/eidrogo/work-management-api/internal/metrics"
	"github.com/eidrogo/work-management-api/internal/notifications"
	"github.com/eidrogo/work-management-api/internal/platform/clock"
	"github.com/eidrogo/work-management-api/internal/platform/database"
	"github.com/eidrogo/work-management-api/internal/platform/id"
	"github.com/eidrogo/work-management-api/internal/platform/logging"
	"github.com/eidrogo/work-management-api/internal/workitems"
)

func main() {
	if err := run(); err != nil {
		log.Printf("api terminated: %v", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.LoadAPI(nil)
	if err != nil {
		return fmt.Errorf("load API configuration: %w", err)
	}
	logger, err := logging.New(cfg.LogLevel, os.Stdout)
	if err != nil {
		return err
	}
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(rootCtx, cfg.Database.URL, "work-management-api", cfg.Database.MaxConns)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	authService := auth.NewService(auth.PostgresTokenStore{DB: db}, clock.System{})
	workService, err := workitems.NewService(db, clock.System{}, id.NewUUID)
	if err != nil {
		return err
	}
	store, err := notifications.NewStore(db)
	if err != nil {
		return err
	}
	notificationService, err := notifications.NewService(store)
	if err != nil {
		return err
	}
	metricRegistry := metrics.New(store, time.Now)
	handler, err := httpapi.NewServer(httpapi.ServerConfig{Logger: logger, Authenticator: authService, WorkItems: workService, Notifications: notificationService, Readiness: func(ctx context.Context) error { return database.Ready(ctx, db) }, RequestID: id.NewUUID, MaxBodyBytes: cfg.MaxBodyBytes, Metrics: metricRegistry, MetricRecorder: metricRegistry})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: cfg.HTTPAddress, Handler: handler, ReadHeaderTimeout: cfg.ReadHeaderTimeout, ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout, ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError)}
	serveErrors := make(chan error, 1)
	go func() {
		logger.Info("api listening", "address", cfg.HTTPAddress)
		serveErrors <- server.ListenAndServe()
	}()
	select {
	case <-rootCtx.Done():
	case serveErr := <-serveErrors:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err = server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return err
	}
	return nil
}
