package config

import (
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type LookupEnv func(string) (string, bool)
type Environment string
type TLSMode string

const (
	EnvironmentDevelopment Environment = "development"
	EnvironmentTest        Environment = "test"
	EnvironmentProduction  Environment = "production"
	TLSModePlain           TLSMode     = "plain"
	TLSModeSTARTTLS        TLSMode     = "starttls"
	TLSModeImplicit        TLSMode     = "tls"
)

type Database struct {
	URL      string
	MaxConns int
}

type API struct {
	Environment       Environment
	Database          Database
	HTTPAddress       string
	AuthMode          string
	MaxBodyBytes      int64
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	LogLevel          string
}

type SMTP struct {
	Host                  string
	Port                  int
	Username              string
	Password              string
	From                  string
	FromName              string
	TLSMode               TLSMode
	TLSServerName         string
	TLSInsecureSkipVerify bool
	Timeout               time.Duration
}

type Worker struct {
	Environment     Environment
	Database        Database
	HTTPAddress     string
	WorkerID        string
	PollInterval    time.Duration
	BatchSize       int
	LeaseDuration   time.Duration
	MaxAttempts     int
	BackoffBase     time.Duration
	BackoffMax      time.Duration
	ShutdownTimeout time.Duration
	LogLevel        string
	SMTP            SMTP
}

type Command struct {
	Environment   Environment
	Database      Database
	MigrationsDir string
}

func LoadAPI(lookup LookupEnv) (API, error) {
	lookup = normalize(lookup)
	environment, err := environmentValue(lookup)
	if err != nil {
		return API{}, err
	}
	database, err := databaseValue(lookup, environment)
	if err != nil {
		return API{}, err
	}
	authMode := strings.ToLower(value(lookup, "AUTH_MODE", "development"))
	if environment == EnvironmentProduction && authMode == "development" {
		return API{}, fmt.Errorf("development authentication is not allowed in production")
	}
	if authMode != "development" {
		return API{}, fmt.Errorf("AUTH_MODE %q is unsupported; only development is implemented", authMode)
	}
	maxBody, err := int64Value(lookup, "HTTP_MAX_BODY_BYTES", 1<<20, 1024, 10<<20)
	if err != nil {
		return API{}, err
	}
	readHeader, err := durationValue(lookup, "HTTP_READ_HEADER_TIMEOUT", 5*time.Second, time.Second, time.Minute)
	if err != nil {
		return API{}, err
	}
	read, err := durationValue(lookup, "HTTP_READ_TIMEOUT", 15*time.Second, time.Second, 5*time.Minute)
	if err != nil {
		return API{}, err
	}
	write, err := durationValue(lookup, "HTTP_WRITE_TIMEOUT", 30*time.Second, time.Second, 5*time.Minute)
	if err != nil {
		return API{}, err
	}
	idle, err := durationValue(lookup, "HTTP_IDLE_TIMEOUT", time.Minute, time.Second, 10*time.Minute)
	if err != nil {
		return API{}, err
	}
	shutdown, err := durationValue(lookup, "SHUTDOWN_TIMEOUT", 10*time.Second, time.Second, 5*time.Minute)
	if err != nil {
		return API{}, err
	}
	level, err := logLevel(lookup)
	if err != nil {
		return API{}, err
	}
	return API{
		Environment: environment, Database: database, HTTPAddress: value(lookup, "HTTP_ADDR", ":8080"),
		AuthMode: authMode, MaxBodyBytes: maxBody, ReadHeaderTimeout: readHeader,
		ReadTimeout: read, WriteTimeout: write, IdleTimeout: idle,
		ShutdownTimeout: shutdown, LogLevel: level,
	}, nil
}

func LoadWorker(lookup LookupEnv) (Worker, error) {
	lookup = normalize(lookup)
	environment, err := environmentValue(lookup)
	if err != nil {
		return Worker{}, err
	}
	database, err := databaseValue(lookup, environment)
	if err != nil {
		return Worker{}, err
	}
	host, err := required(lookup, "SMTP_HOST")
	if err != nil {
		return Worker{}, err
	}
	from, err := required(lookup, "SMTP_FROM")
	if err != nil {
		return Worker{}, err
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return Worker{}, fmt.Errorf("SMTP_FROM must be a valid address: %w", err)
	}
	username := value(lookup, "SMTP_USERNAME", "")
	password := value(lookup, "SMTP_PASSWORD", "")
	if (username == "") != (password == "") {
		return Worker{}, fmt.Errorf("SMTP_USERNAME and SMTP_PASSWORD must be configured together")
	}
	tlsMode := TLSMode(strings.ToLower(value(lookup, "SMTP_TLS_MODE", string(TLSModeSTARTTLS))))
	switch tlsMode {
	case TLSModePlain, TLSModeSTARTTLS, TLSModeImplicit:
	default:
		return Worker{}, fmt.Errorf("SMTP_TLS_MODE must be plain, starttls, or tls")
	}
	if environment == EnvironmentProduction && tlsMode == TLSModePlain {
		return Worker{}, fmt.Errorf("plain SMTP is not allowed in production")
	}
	insecure, err := boolValue(lookup, "SMTP_TLS_INSECURE_SKIP_VERIFY", false)
	if err != nil {
		return Worker{}, err
	}
	if environment == EnvironmentProduction && insecure {
		return Worker{}, fmt.Errorf("SMTP_TLS_INSECURE_SKIP_VERIFY is forbidden in production")
	}
	port, err := intValue(lookup, "SMTP_PORT", 1025, 1, 65535)
	if err != nil {
		return Worker{}, err
	}
	timeout, err := durationValue(lookup, "SMTP_TIMEOUT", 10*time.Second, time.Second, 5*time.Minute)
	if err != nil {
		return Worker{}, err
	}
	lease, err := durationValue(lookup, "LEASE_DURATION", 30*time.Second, time.Second, 30*time.Minute)
	if err != nil {
		return Worker{}, err
	}
	if lease <= timeout {
		return Worker{}, fmt.Errorf("LEASE_DURATION must exceed SMTP_TIMEOUT")
	}
	poll, err := durationValue(lookup, "WORKER_POLL_INTERVAL", time.Second, 50*time.Millisecond, time.Minute)
	if err != nil {
		return Worker{}, err
	}
	batch, err := intValue(lookup, "WORKER_BATCH_SIZE", 10, 1, 100)
	if err != nil {
		return Worker{}, err
	}
	attempts, err := intValue(lookup, "MAX_ATTEMPTS", 5, 1, 20)
	if err != nil {
		return Worker{}, err
	}
	base, err := durationValue(lookup, "RETRY_BACKOFF_BASE", 2*time.Second, 100*time.Millisecond, time.Hour)
	if err != nil {
		return Worker{}, err
	}
	maximum, err := durationValue(lookup, "RETRY_BACKOFF_MAX", time.Hour, base, 24*time.Hour)
	if err != nil {
		return Worker{}, err
	}
	shutdown, err := durationValue(lookup, "SHUTDOWN_TIMEOUT", 10*time.Second, time.Second, 5*time.Minute)
	if err != nil {
		return Worker{}, err
	}
	level, err := logLevel(lookup)
	if err != nil {
		return Worker{}, err
	}
	workerID := value(lookup, "WORKER_ID", "")
	if workerID == "" {
		hostname, hostErr := os.Hostname()
		if hostErr != nil || hostname == "" {
			hostname = "unknown-host"
		}
		workerID = fmt.Sprintf("%s-%d", hostname, os.Getpid())
	}
	return Worker{
		Environment: environment, Database: database, HTTPAddress: value(lookup, "WORKER_HTTP_ADDR", ":9091"), WorkerID: workerID,
		PollInterval: poll, BatchSize: batch, LeaseDuration: lease, MaxAttempts: attempts,
		BackoffBase: base, BackoffMax: maximum, ShutdownTimeout: shutdown, LogLevel: level,
		SMTP: SMTP{Host: host, Port: port, Username: username, Password: password, From: from,
			FromName: value(lookup, "SMTP_FROM_NAME", "Work Management"), TLSMode: tlsMode,
			TLSServerName: value(lookup, "SMTP_TLS_SERVER_NAME", host), TLSInsecureSkipVerify: insecure, Timeout: timeout},
	}, nil
}

func LoadCommand(lookup LookupEnv) (Command, error) {
	lookup = normalize(lookup)
	environment, err := environmentValue(lookup)
	if err != nil {
		return Command{}, err
	}
	database, err := databaseValue(lookup, environment)
	if err != nil {
		return Command{}, err
	}
	return Command{Environment: environment, Database: database, MigrationsDir: value(lookup, "MIGRATIONS_DIR", "migrations")}, nil
}

func normalize(lookup LookupEnv) LookupEnv {
	if lookup == nil {
		return os.LookupEnv
	}
	return lookup
}
func environmentValue(lookup LookupEnv) (Environment, error) {
	environment := Environment(strings.ToLower(value(lookup, "APP_ENV", string(EnvironmentDevelopment))))
	switch environment {
	case EnvironmentDevelopment, EnvironmentTest, EnvironmentProduction:
		return environment, nil
	default:
		return "", fmt.Errorf("APP_ENV must be development, test, or production")
	}
}
func databaseValue(lookup LookupEnv, environment Environment) (Database, error) {
	databaseURL, err := required(lookup, "DATABASE_URL")
	if err != nil {
		return Database{}, err
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
		return Database{}, fmt.Errorf("DATABASE_URL must be a valid postgres URL with host and database name")
	}
	if environment == EnvironmentProduction && strings.ToLower(parsed.Query().Get("sslmode")) != "verify-full" {
		return Database{}, fmt.Errorf("DATABASE_URL must use sslmode=verify-full in production")
	}
	maximum, err := intValue(lookup, "DATABASE_MAX_CONNS", 10, 1, 100)
	if err != nil {
		return Database{}, err
	}
	return Database{URL: databaseURL, MaxConns: maximum}, nil
}
func required(lookup LookupEnv, key string) (string, error) {
	result := value(lookup, key, "")
	if result == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return result, nil
}
func value(lookup LookupEnv, key, fallback string) string {
	if result, ok := lookup(key); ok {
		return strings.TrimSpace(result)
	}
	return fallback
}
func intValue(lookup LookupEnv, key string, fallback, minimum, maximum int) (int, error) {
	raw := value(lookup, key, "")
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", key, minimum, maximum)
	}
	return parsed, nil
}
func int64Value(lookup LookupEnv, key string, fallback, minimum, maximum int64) (int64, error) {
	raw := value(lookup, key, "")
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", key, minimum, maximum)
	}
	return parsed, nil
}
func durationValue(lookup LookupEnv, key string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	raw := value(lookup, key, "")
	if raw == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be a duration between %s and %s", key, minimum, maximum)
	}
	return parsed, nil
}
func boolValue(lookup LookupEnv, key string, fallback bool) (bool, error) {
	raw := value(lookup, key, "")
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return parsed, nil
}
func logLevel(lookup LookupEnv) (string, error) {
	level := strings.ToLower(value(lookup, "LOG_LEVEL", "info"))
	switch level {
	case "debug", "info", "warn", "error":
		return level, nil
	default:
		return "", fmt.Errorf("LOG_LEVEL must be debug, info, warn, or error")
	}
}
