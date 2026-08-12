package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func openIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()

	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := Open(ctx, databaseURL, "work-management-database-test", 4)
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenConfiguresPostgreSQLSession(t *testing.T) {
	db := openIntegrationDB(t)

	var applicationName, timezone string
	if err := db.QueryRowContext(context.Background(), `SELECT current_setting('application_name'), current_setting('TimeZone')`).Scan(&applicationName, &timezone); err != nil {
		t.Fatal(err)
	}
	if applicationName != "work-management-database-test" {
		t.Fatalf("application_name=%q", applicationName)
	}
	if timezone != "UTC" {
		t.Fatalf("TimeZone=%q", timezone)
	}
}

func TestOpenEnforcesUTCAgainstMixedCaseTimeZone(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("TimeZone", "America/Argentina/Buenos_Aires")
	parsed.RawQuery = query.Encode()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := Open(ctx, parsed.String(), "work-management-timezone-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var timezone string
	if err := db.QueryRowContext(ctx, `SHOW TimeZone`).Scan(&timezone); err != nil {
		t.Fatal(err)
	}
	if timezone != "UTC" {
		t.Fatalf("TimeZone=%q want UTC", timezone)
	}
}

func TestDatabaseSQLScansPostgreSQLTypes(t *testing.T) {
	db := openIntegrationDB(t)

	const wantUUID = "550e8400-e29b-41d4-a716-446655440000"
	var (
		integerValue int64
		boolValue    bool
		jsonValue    []byte
		uuidValue    string
		timestamp    time.Time
	)
	if err := db.QueryRowContext(context.Background(), `
		SELECT 42::bigint, true::boolean, '{"ok":true}'::jsonb,
		       '550e8400-e29b-41d4-a716-446655440000'::uuid,
		       '2026-08-12 12:00:00-03'::timestamptz
	`).Scan(&integerValue, &boolValue, &jsonValue, &uuidValue, &timestamp); err != nil {
		t.Fatal(err)
	}
	if integerValue != 42 || !boolValue || uuidValue != wantUUID {
		t.Fatalf("values integer=%d bool=%v uuid=%q", integerValue, boolValue, uuidValue)
	}
	var decoded map[string]bool
	if err := json.Unmarshal(jsonValue, &decoded); err != nil || !decoded["ok"] {
		t.Fatalf("json=%q err=%v", jsonValue, err)
	}
	if timestamp.Location() != time.UTC {
		t.Fatalf("timestamp location=%v want UTC", timestamp.Location())
	}
	if want := time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC); !timestamp.Equal(want) {
		t.Fatalf("timestamp=%s want %s", timestamp, want)
	}
}

func TestDatabaseSQLSupportsRepeatableReadOnlyTransaction(t *testing.T) {
	db := openIntegrationDB(t)

	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var isolation, readOnly string
	if err := tx.QueryRowContext(context.Background(), `SELECT current_setting('transaction_isolation'), current_setting('transaction_read_only')`).Scan(&isolation, &readOnly); err != nil {
		t.Fatal(err)
	}
	if isolation != "repeatable read" || readOnly != "on" {
		t.Fatalf("isolation=%q readOnly=%q", isolation, readOnly)
	}
}

func TestCancellationKeepsPoolHealthy(t *testing.T) {
	db := openIntegrationDB(t)

	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()
	err := db.QueryRowContext(ctx, `SELECT pg_sleep(5)`).Scan(new(any))
	if err == nil {
		t.Fatal("expected canceled query")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("query error=%v context error=%v", err, ctx.Err())
	}

	healthCtx, healthCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer healthCancel()
	var one int
	if err := db.QueryRowContext(healthCtx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("pool did not recover after cancellation: %v", err)
	}
	if one != 1 {
		t.Fatalf("health query=%d", one)
	}
}
