package database

import (
	"net/url"
	"testing"
)

func TestBuildDSNAddsOperationalParameters(t *testing.T) {
	t.Parallel()
	dsn, err := BuildDSN("postgres://user:secret@localhost:5432/work?sslmode=disable", "work-management-api")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(dsn)
	query := parsed.Query()
	if query.Get("application_name") != "work-management-api" || query.Get("options") != "-c timezone=UTC" {
		t.Fatalf("unexpected query: %v", query)
	}
}
