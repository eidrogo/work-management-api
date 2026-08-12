package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
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

func BuildConfig(databaseURL, applicationName string) (*pgx.ConnConfig, error) {
	if strings.TrimSpace(databaseURL) == "" || strings.TrimSpace(applicationName) == "" {
		return nil, fmt.Errorf("DATABASE_URL and application name are required")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
		return nil, fmt.Errorf("DATABASE_URL must be a valid postgres URL with host and database name")
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil || config.Host == "" || config.Database == "" {
		return nil, fmt.Errorf("DATABASE_URL must be a valid postgres URL with host and database name")
	}
	config.RuntimeParams["application_name"] = applicationName
	for key := range config.RuntimeParams {
		if strings.EqualFold(key, "timezone") {
			delete(config.RuntimeParams, key)
		}
	}
	options := strings.TrimSpace(config.RuntimeParams["options"])
	if options != "" {
		options += " "
	}
	config.RuntimeParams["options"] = options + "-c timezone=UTC"
	if !parsed.Query().Has("connect_timeout") {
		config.ConnectTimeout = 5 * time.Second
	}
	config.DefaultQueryExecMode = pgx.QueryExecModeExec
	return config, nil
}

func Open(ctx context.Context, databaseURL, applicationName string, maxOpenConns int) (*sql.DB, error) {
	cfg, err := DefaultPoolConfig(maxOpenConns)
	if err != nil {
		return nil, err
	}
	config, err := BuildConfig(databaseURL, applicationName)
	if err != nil {
		return nil, err
	}
	db := stdlib.OpenDB(*config, stdlib.OptionAfterConnect(func(_ context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterType(&pgtype.Type{
			Name:  "timestamptz",
			OID:   pgtype.TimestamptzOID,
			Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}))
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
