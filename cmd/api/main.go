package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eidrogo/work-management-api/internal/apiadmin"
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

type serverEndpoint struct {
	name     string
	server   *http.Server
	listener net.Listener
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
	publicHandler, err := httpapi.NewServer(httpapi.ServerConfig{Logger: logger, Authenticator: authService, WorkItems: workService, Notifications: notificationService, RequestID: id.NewUUID, MaxBodyBytes: cfg.MaxBodyBytes, MetricRecorder: metricRegistry})
	if err != nil {
		return err
	}
	adminHandler, err := apiadmin.New(apiadmin.Config{Logger: logger, Readiness: func(ctx context.Context) error { return database.Ready(ctx, db) }, Metrics: metricRegistry})
	if err != nil {
		return err
	}
	errorLog := slog.NewLogLogger(logger.Handler(), slog.LevelError)
	publicServer := &http.Server{Addr: cfg.HTTPAddress, Handler: publicHandler, ReadHeaderTimeout: cfg.ReadHeaderTimeout, ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout, ErrorLog: errorLog}
	adminServer := &http.Server{Addr: cfg.AdminHTTPAddress, Handler: adminHandler, ReadHeaderTimeout: cfg.ReadHeaderTimeout, ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout, ErrorLog: errorLog}
	publicListener, err := net.Listen("tcp", cfg.HTTPAddress)
	if err != nil {
		return fmt.Errorf("bind public API listener: %w", err)
	}
	adminListener, err := net.Listen("tcp", cfg.AdminHTTPAddress)
	if err != nil {
		bindErr := fmt.Errorf("bind API admin listener: %w", err)
		if closeErr := publicListener.Close(); closeErr != nil {
			return errors.Join(bindErr, fmt.Errorf("close public API listener after admin bind failure: %w", closeErr))
		}
		return bindErr
	}
	logger.Info("api public listener bound", "configured_address", cfg.HTTPAddress, "resolved_address", publicListener.Addr().String())
	logger.Info("api admin listener bound", "configured_address", cfg.AdminHTTPAddress, "resolved_address", adminListener.Addr().String())
	return serveEndpoints(rootCtx, cfg.ShutdownTimeout,
		serverEndpoint{name: "public API", server: publicServer, listener: publicListener},
		serverEndpoint{name: "API admin", server: adminServer, listener: adminListener},
	)
}

func serveEndpoints(ctx context.Context, shutdownTimeout time.Duration, endpoints ...serverEndpoint) error {
	type endpointResult struct {
		endpoint serverEndpoint
		err      error
	}
	serveResults := make(chan endpointResult, len(endpoints))
	for _, endpoint := range endpoints {
		endpoint := endpoint
		go func() {
			serveResults <- endpointResult{endpoint: endpoint, err: endpoint.server.Serve(endpoint.listener)}
		}()
	}

	remaining := len(endpoints)
	var result error
	select {
	case <-ctx.Done():
	case serveResult := <-serveResults:
		remaining--
		if errors.Is(serveResult.err, http.ErrServerClosed) {
			result = fmt.Errorf("%s listener stopped before context cancellation", serveResult.endpoint.name)
		} else if serveResult.err != nil {
			result = fmt.Errorf("%s listener: %w", serveResult.endpoint.name, serveResult.err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownResults := make(chan endpointResult, len(endpoints))
	for _, endpoint := range endpoints {
		endpoint := endpoint
		go func() {
			shutdownResults <- endpointResult{endpoint: endpoint, err: endpoint.server.Shutdown(shutdownCtx)}
		}()
	}
	for range endpoints {
		shutdownResult := <-shutdownResults
		if shutdownResult.err == nil {
			continue
		}
		result = errors.Join(result, fmt.Errorf("shut down %s listener: %w", shutdownResult.endpoint.name, shutdownResult.err))
		if closeErr := shutdownResult.endpoint.server.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
			result = errors.Join(result, fmt.Errorf("force close %s listener: %w", shutdownResult.endpoint.name, closeErr))
		}
	}

	for ; remaining > 0; remaining-- {
		serveResult := <-serveResults
		if serveResult.err != nil && !errors.Is(serveResult.err, http.ErrServerClosed) {
			result = errors.Join(result, fmt.Errorf("%s listener: %w", serveResult.endpoint.name, serveResult.err))
		}
	}
	return result
}
