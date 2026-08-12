package migrations

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverOrdersAndPairsMigrations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"000002_second.down.sql": "DROP TABLE second;",
		"000001_first.up.sql":    "CREATE TABLE first(id int);",
		"000002_second.up.sql":   "CREATE TABLE second(id int);",
		"000001_first.down.sql":  "DROP TABLE first;",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Version != 1 || got[1].Version != 2 || got[0].Checksum == "" {
		t.Fatalf("got %#v", got)
	}
}
