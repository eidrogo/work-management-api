package config

import (
	"strings"
	"testing"

	"github.com/eidrogo/work-management-api/internal/notifications"
)

func TestLoadWorkerValidatesPersistedWorkerIDContract(t *testing.T) {
	tests := []struct {
		name      string
		workerID  string
		wantError bool
	}{
		{name: "255 ASCII", workerID: strings.Repeat("a", 255)},
		{name: "255 multibyte runes", workerID: strings.Repeat("é", 255)},
		{name: "256 runes", workerID: strings.Repeat("a", 256), wantError: true},
		{name: "blank", workerID: "   ", wantError: true},
		{name: "control", workerID: "worker\nid", wantError: true},
		{name: "invalid UTF-8", workerID: string([]byte{'w', 0xff}), wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := workerValues()
			values["WORKER_ID"] = test.workerID
			worker, err := LoadWorker(mapLookup(values))
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "WORKER_ID") {
					t.Fatalf("worker=%#v error=%v", worker, err)
				}
				return
			}
			if err != nil || worker.WorkerID != test.workerID {
				t.Fatalf("worker=%#v error=%v", worker, err)
			}
		})
	}
}

func TestLoadWorkerValidatesEffectiveSMTPFromDisplayName(t *testing.T) {
	tests := []struct {
		name, from, fromName string
		wantError            bool
	}{
		{name: "200 ASCII runes", from: "work@example.test", fromName: strings.Repeat("a", 200)},
		{name: "200 two-byte runes", from: "work@example.test", fromName: strings.Repeat("é", 200)},
		{name: "200 four-byte runes", from: "work@example.test", fromName: strings.Repeat("😀", 200)},
		{name: "201 runes", from: "work@example.test", fromName: strings.Repeat("a", 201), wantError: true},
		{name: "invalid UTF-8", from: "work@example.test", fromName: string([]byte{'b', 0xff}), wantError: true},
		{name: "NUL control", from: "work@example.test", fromName: "bad\x00name", wantError: true},
		{name: "tab control", from: "work@example.test", fromName: "bad\tname", wantError: true},
		{name: "trimmed tab control", from: "work@example.test", fromName: "\tWork Management", wantError: true},
		{name: "C1 control", from: "work@example.test", fromName: "bad\u0085name", wantError: true},
		{name: "invalid embedded effective name", from: "=?utf-8?q?bad=00name?= <work@example.test>", fromName: "", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := workerValues()
			values["SMTP_FROM"] = test.from
			values["SMTP_FROM_NAME"] = test.fromName
			worker, err := LoadWorker(mapLookup(values))
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "SMTP_FROM_NAME") {
					t.Fatalf("worker=%#v error=%v", worker, err)
				}
				return
			}
			if err != nil || worker.SMTP.FromName != test.fromName {
				t.Fatalf("worker=%#v error=%v", worker, err)
			}
		})
	}
}

func TestGeneratedWorkerIDIsValidForLongHostname(t *testing.T) {
	generated := generatedWorkerID(strings.Repeat("é", 400), 12345)
	if len([]rune(generated)) > 255 {
		t.Fatalf("worker ID has %d runes", len([]rune(generated)))
	}
	if got, err := notifications.ValidateWorkerID(generated); err != nil || got != generated {
		t.Fatalf("worker ID=%q validation=%q error=%v", generated, got, err)
	}
}

func TestGeneratedWorkerIDSanitizesInvalidHostnameWithoutBreakingUTF8(t *testing.T) {
	generated := generatedWorkerID(string([]byte{'h', 0xff, '\n', 'x'}), 7)
	if generated != "h-x-7" {
		t.Fatalf("worker ID=%q", generated)
	}
	if got, err := notifications.ValidateWorkerID(generated); err != nil || got != generated {
		t.Fatalf("worker ID=%q validation=%q error=%v", generated, got, err)
	}
}

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

func TestLoadWorkerProductionSMTPGuards(t *testing.T) {
	base := map[string]string{
		"APP_ENV":              "production",
		"DATABASE_URL":         "postgres://db.example.test/work?sslmode=verify-full",
		"SMTP_HOST":            "smtp.example.test",
		"SMTP_PORT":            "587",
		"SMTP_FROM":            "work@example.test",
		"SMTP_TLS_MODE":        "starttls",
		"SMTP_TLS_SERVER_NAME": "smtp.example.test",
		"SMTP_TIMEOUT":         "10s",
		"LEASE_DURATION":       "11s",
	}
	tests := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{name: "plain TLS", values: map[string]string{"SMTP_TLS_MODE": "plain"}, want: "plain SMTP is not allowed"},
		{name: "username only", values: map[string]string{"SMTP_USERNAME": "user"}, want: "configured together"},
		{name: "password only", values: map[string]string{"SMTP_PASSWORD": "secret"}, want: "configured together"},
		{name: "verification bypass", values: map[string]string{"SMTP_TLS_INSECURE_SKIP_VERIFY": "true"}, want: "forbidden in production"},
		{name: "lease equals timeout", values: map[string]string{"LEASE_DURATION": "10s"}, want: "must exceed SMTP_TIMEOUT"},
		{name: "verified STARTTLS"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := make(map[string]string, len(base)+len(test.values))
			for key, value := range base {
				values[key] = value
			}
			for key, value := range test.values {
				values[key] = value
			}
			config, err := LoadWorker(mapLookup(values))
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if config.SMTP.TLSMode != TLSModeSTARTTLS || config.SMTP.TLSInsecureSkipVerify || config.SMTP.TLSServerName != "smtp.example.test" {
					t.Fatalf("SMTP=%#v", config.SMTP)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want substring=%q", err, test.want)
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
