package config

import (
	"strings"
	"testing"
)

func TestLoadAPIDefaultsIncludeLoopbackAdmin(t *testing.T) {
	t.Parallel()
	cfg, err := LoadAPI(mapLookup(map[string]string{"DATABASE_URL": "postgres://localhost/work?sslmode=disable"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddress != ":8080" || cfg.AdminHTTPAddress != "127.0.0.1:9090" || cfg.MaxBodyBytes != 1<<20 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestLoadWorkerDefaultsToLoopbackAdmin(t *testing.T) {
	t.Parallel()
	cfg, err := LoadWorker(mapLookup(workerValues()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddress != "127.0.0.1:9091" {
		t.Fatalf("HTTPAddress=%q", cfg.HTTPAddress)
	}
}

func TestLoadAPIListenerOverrides(t *testing.T) {
	t.Parallel()
	cfg, err := LoadAPI(mapLookup(map[string]string{
		"DATABASE_URL":   "postgres://localhost/work?sslmode=disable",
		"HTTP_ADDR":      "127.0.0.1:18080",
		"API_ADMIN_ADDR": "[::1]:19090",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddress != "127.0.0.1:18080" || cfg.AdminHTTPAddress != "[::1]:19090" {
		t.Fatalf("listeners=%q,%q", cfg.HTTPAddress, cfg.AdminHTTPAddress)
	}
}

func TestLoadWorkerListenerOverride(t *testing.T) {
	t.Parallel()
	values := workerValues()
	values["WORKER_HTTP_ADDR"] = ":19091"
	cfg, err := LoadWorker(mapLookup(values))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddress != ":19091" {
		t.Fatalf("HTTPAddress=%q", cfg.HTTPAddress)
	}
}

func TestLoadRejectsInvalidListenerAddresses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		key   string
		value string
		api   bool
	}{
		{name: "public missing port", key: "HTTP_ADDR", value: "localhost", api: true},
		{name: "admin nonnumeric port", key: "API_ADMIN_ADDR", value: "localhost:http", api: true},
		{name: "admin signed port", key: "API_ADMIN_ADDR", value: "localhost:+9090", api: true},
		{name: "public zero port", key: "HTTP_ADDR", value: ":0", api: true},
		{name: "admin port too large", key: "API_ADMIN_ADDR", value: "127.0.0.1:65536", api: true},
		{name: "public unbracketed IPv6", key: "HTTP_ADDR", value: "::1:8080", api: true},
		{name: "worker explicit empty", key: "WORKER_HTTP_ADDR", value: "", api: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if test.api {
				values := map[string]string{"DATABASE_URL": "postgres://localhost/work?sslmode=disable", test.key: test.value}
				if _, err := LoadAPI(mapLookup(values)); err == nil || !strings.Contains(err.Error(), test.key+" must be a valid TCP host:port address") {
					t.Fatalf("error=%v", err)
				}
				return
			}
			values := workerValues()
			values[test.key] = test.value
			if _, err := LoadWorker(mapLookup(values)); err == nil || !strings.Contains(err.Error(), test.key+" must be a valid TCP host:port address") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestLoadAPIRejectsIdenticalListenerAddresses(t *testing.T) {
	t.Parallel()
	_, err := LoadAPI(mapLookup(map[string]string{
		"DATABASE_URL":   "postgres://localhost/work?sslmode=disable",
		"HTTP_ADDR":      "127.0.0.1:18080",
		"API_ADMIN_ADDR": "127.0.0.1:18080",
	}))
	if err == nil || err.Error() != "HTTP_ADDR and API_ADMIN_ADDR must be different" {
		t.Fatalf("error=%v", err)
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

func workerValues() map[string]string {
	return map[string]string{
		"DATABASE_URL":  "postgres://localhost/work?sslmode=disable",
		"SMTP_HOST":     "smtp.example.test",
		"SMTP_FROM":     "work@example.test",
		"SMTP_TLS_MODE": "plain",
	}
}
