package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var pattern = regexp.MustCompile(`^(\d{6})_([a-z0-9_]+)\.(up|down)\.sql$`)

type Migration struct {
	Version  int64
	Name     string
	UpSQL    string
	DownSQL  string
	Checksum string
}

type Applied struct {
	Version int64
	Name    string
}

func Discover(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	type partial struct{ name, up, down string }
	parts := map[int64]*partial{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		match := pattern.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("malformed migration filename %q", entry.Name())
		}
		version, _ := strconv.ParseInt(match[1], 10, 64)
		part := parts[version]
		if part == nil {
			part = &partial{name: match[2]}
			parts[version] = part
		}
		if part.name != match[2] {
			return nil, fmt.Errorf("migration version %06d has multiple names", version)
		}
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(string(content)) == "" {
			return nil, fmt.Errorf("empty migration %q", entry.Name())
		}
		if match[3] == "up" {
			part.up = string(content)
		} else {
			part.down = string(content)
		}
	}
	versions := make([]int64, 0, len(parts))
	for version := range parts {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	result := make([]Migration, 0, len(versions))
	for _, version := range versions {
		part := parts[version]
		if part.up == "" || part.down == "" {
			return nil, fmt.Errorf("migration %06d requires up and down files", version)
		}
		sum := sha256.Sum256([]byte(part.up))
		result = append(result, Migration{version, part.name, part.up, part.down, hex.EncodeToString(sum[:])})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no migrations found")
	}
	return result, nil
}

type Runner struct {
	DB  *sql.DB
	Dir string
}

func (runner Runner) Up(ctx context.Context) ([]Applied, error) {
	items, err := Discover(runner.Dir)
	if err != nil {
		return nil, err
	}
	conn, cleanup, err := runner.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if err := ensureTable(ctx, conn); err != nil {
		return nil, err
	}
	applied, err := readApplied(ctx, conn)
	if err != nil {
		return nil, err
	}
	var changed []Applied
	for _, item := range items {
		if checksum, ok := applied[item.Version]; ok {
			if checksum != item.Checksum {
				return nil, fmt.Errorf("migration %06d checksum changed", item.Version)
			}
			continue
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, item.UpSQL); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("apply %06d_%s: %w", item.Version, item.Name, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,checksum,applied_at) VALUES($1,$2,$3,$4)`, item.Version, item.Name, item.Checksum, time.Now().UTC()); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		changed = append(changed, Applied{item.Version, item.Name})
	}
	return changed, nil
}

func (runner Runner) Down(ctx context.Context, steps int) ([]Applied, error) {
	if steps < 1 {
		return nil, fmt.Errorf("steps must be positive")
	}
	items, err := Discover(runner.Dir)
	if err != nil {
		return nil, err
	}
	byVersion := map[int64]Migration{}
	for _, item := range items {
		byVersion[item.Version] = item
	}
	conn, cleanup, err := runner.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if err = ensureTable(ctx, conn); err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, `SELECT version,name FROM schema_migrations ORDER BY version DESC LIMIT $1`, steps)
	if err != nil {
		return nil, err
	}
	var targets []Applied
	for rows.Next() {
		var target Applied
		if err = rows.Scan(&target.Version, &target.Name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		targets = append(targets, target)
	}
	_ = rows.Close()
	var changed []Applied
	for _, target := range targets {
		item, ok := byVersion[target.Version]
		if !ok {
			return nil, fmt.Errorf("missing down migration %06d", target.Version)
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, item.DownSQL); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=$1`, target.Version); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		changed = append(changed, target)
	}
	return changed, nil
}

func (runner Runner) lock(ctx context.Context) (*sql.Conn, func(), error) {
	if runner.DB == nil {
		return nil, nil, fmt.Errorf("database required")
	}
	conn, err := runner.DB.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(781842031119)`); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(781842031119)`)
		_ = conn.Close()
	}, nil
}
func ensureTable(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version BIGINT PRIMARY KEY,name TEXT NOT NULL,checksum CHAR(64) NOT NULL,applied_at TIMESTAMPTZ NOT NULL)`)
	return err
}
func readApplied(ctx context.Context, conn *sql.Conn) (map[int64]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version,checksum FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64]string{}
	for rows.Next() {
		var version int64
		var checksum string
		if err = rows.Scan(&version, &checksum); err != nil {
			return nil, err
		}
		result[version] = checksum
	}
	return result, rows.Err()
}
