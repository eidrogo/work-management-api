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

func mapLookup(values map[string]string) LookupEnv {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}
