package database

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestBuildConfigAddsOperationalParameters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		databaseURL     string
		applicationName string
		wantOptions     string
		wantTimeout     time.Duration
	}{
		{
			name:            "defaults",
			databaseURL:     "postgres://user:secret@localhost:5432/work?sslmode=disable",
			applicationName: "work-management-api",
			wantOptions:     "-c timezone=UTC",
			wantTimeout:     5 * time.Second,
		},
		{
			name:            "overrides application name and preserves options",
			databaseURL:     "postgres://user:secret@localhost:5432/work?sslmode=disable&application_name=old&options=-c%20statement_timeout%3D1000&timezone=America%2FArgentina%2FBuenos_Aires",
			applicationName: "work-management-worker",
			wantOptions:     "-c statement_timeout=1000 -c timezone=UTC",
			wantTimeout:     5 * time.Second,
		},
		{
			name:            "explicit connect timeout",
			databaseURL:     "postgres://user:secret@localhost:5432/work?sslmode=disable&connect_timeout=12",
			applicationName: "work-management-migrate",
			wantOptions:     "-c timezone=UTC",
			wantTimeout:     12 * time.Second,
		},
		{
			name:            "explicit zero connect timeout",
			databaseURL:     "postgres://user:secret@localhost:5432/work?sslmode=disable&connect_timeout=0",
			applicationName: "work-management-seed",
			wantOptions:     "-c timezone=UTC",
			wantTimeout:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config, err := BuildConfig(tt.databaseURL, tt.applicationName)
			if err != nil {
				t.Fatal(err)
			}
			if got := config.RuntimeParams["application_name"]; got != tt.applicationName {
				t.Fatalf("application_name=%q want %q", got, tt.applicationName)
			}
			if got := config.RuntimeParams["options"]; got != tt.wantOptions {
				t.Fatalf("options=%q want %q", got, tt.wantOptions)
			}
			if _, exists := config.RuntimeParams["timezone"]; exists {
				t.Fatalf("standalone timezone runtime parameter was retained: %v", config.RuntimeParams)
			}
			if config.ConnectTimeout != tt.wantTimeout {
				t.Fatalf("connect timeout=%s want %s", config.ConnectTimeout, tt.wantTimeout)
			}
			if config.DefaultQueryExecMode != pgx.QueryExecModeExec {
				t.Fatalf("query execution mode=%v want %v", config.DefaultQueryExecMode, pgx.QueryExecModeExec)
			}
		})
	}
}

func TestBuildConfigRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name            string
		databaseURL     string
		applicationName string
	}{
		{name: "blank URL", applicationName: "work-management-api"},
		{name: "blank application name", databaseURL: "postgres://localhost/work"},
		{name: "wrong scheme", databaseURL: "mysql://localhost/work", applicationName: "work-management-api"},
		{name: "missing host", databaseURL: "postgres:///work", applicationName: "work-management-api"},
		{name: "missing database", databaseURL: "postgres://localhost", applicationName: "work-management-api"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := BuildConfig(tt.databaseURL, tt.applicationName); err == nil {
				t.Fatal("expected invalid database configuration error")
			}
		})
	}
}
