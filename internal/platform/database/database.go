package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/eidrogo/work-management-api/internal/platform/postgres"
)

type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func DefaultPoolConfig(maxOpenConns int) (PoolConfig, error) {
	if maxOpenConns < 1 || maxOpenConns > 100 {
		return PoolConfig{}, fmt.Errorf("database maximum connections must be between 1 and 100")
	}
	maxIdle := maxOpenConns / 2
	if maxIdle < 1 {
		maxIdle = 1
	}
	return PoolConfig{maxOpenConns, maxIdle, 30 * time.Minute, 5 * time.Minute}, nil
}

func BuildDSN(databaseURL, applicationName string) (string, error) {
	if strings.TrimSpace(databaseURL) == "" || strings.TrimSpace(applicationName) == "" {
		return "", fmt.Errorf("DATABASE_URL and application name are required")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
		return "", fmt.Errorf("DATABASE_URL must be a valid postgres URL with host and database name")
	}
	query := parsed.Query()
	query.Set("application_name", applicationName)
	query.Del("timezone")
	options := strings.TrimSpace(query.Get("options"))
	if options != "" {
		options += " "
	}
	query.Set("options", options+"-c timezone=UTC")
	if query.Get("connect_timeout") == "" {
		query.Set("connect_timeout", "5")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func Open(ctx context.Context, databaseURL, applicationName string, maxOpenConns int) (*sql.DB, error) {
	cfg, err := DefaultPoolConfig(maxOpenConns)
	if err != nil {
		return nil, err
	}
	dsn, err := BuildDSN(databaseURL, applicationName)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(postgres.DriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return db, nil
}

func Ready(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database is not initialized")
	}
	readyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := db.PingContext(readyCtx); err != nil {
		return fmt.Errorf("PostgreSQL readiness check: %w", err)
	}
	return nil
}
