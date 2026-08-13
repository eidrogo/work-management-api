package config

import (
	"strings"
	"testing"
)

func TestLoadAPIDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := LoadAPI(mapLookup(map[string]string{"DATABASE_URL": "postgres://localhost/work?sslmode=disable"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddress != ":8080" || cfg.MaxBodyBytes != 1<<20 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestProductionRejectsDevelopmentAuthAndInsecureDB(t *testing.T) {
	t.Parallel()
	_, err := LoadAPI(mapLookup(map[string]string{
		"APP_ENV": "production", "DATABASE_URL": "postgres://db/work?sslmode=verify-full", "AUTH_MODE": "development",
	}))
	if err == nil || !strings.Contains(err.Error(), "development authentication") {
		t.Fatalf("error=%v", err)
	}
	_, err = LoadCommand(mapLookup(map[string]string{
		"APP_ENV": "production", "DATABASE_URL": "postgres://db/work?sslmode=disable",
	}))
	if err == nil || !strings.Contains(err.Error(), "verify-full") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadWorkerRequiresLeaseLongerThanOneSMTPTimeout(t *testing.T) {
	t.Parallel()
	base := map[string]string{
		"DATABASE_URL":      "postgres://localhost/work?sslmode=disable",
		"SMTP_HOST":         "smtp.example.test",
		"SMTP_FROM":         "work@example.test",
		"SMTP_TLS_MODE":     "plain",
		"SMTP_TIMEOUT":      "10s",
		"WORKER_BATCH_SIZE": "100",
	}
	for _, test := range []struct {
		name, lease string
		wantError   bool
	}{
		{name: "equal timeout rejected", lease: "10s", wantError: true},
		{name: "one timeout plus one second accepted regardless of batch", lease: "11s"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			values := make(map[string]string, len(base)+1)
			for key, value := range base {
				values[key] = value
			}
			values["LEASE_DURATION"] = test.lease
			_, err := LoadWorker(mapLookup(values))
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}
