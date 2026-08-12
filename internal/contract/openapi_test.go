package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenAPIContract(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract path")
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../api/openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err = json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("openapi=%q", document.OpenAPI)
	}
	for path, methods := range map[string][]string{"/v1/work-items": {"get", "post"}, "/v1/me/work-items": {"get"}, "/v1/notifications": {"get"}, "/health/live": {"get"}, "/health/ready": {"get"}} {
		item, ok := document.Paths[path]
		if !ok {
			t.Errorf("missing path %s", path)
			continue
		}
		for _, method := range methods {
			if _, ok := item[method]; !ok {
				t.Errorf("missing %s %s", method, path)
			}
		}
	}
}
