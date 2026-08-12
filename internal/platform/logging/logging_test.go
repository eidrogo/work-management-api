package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewProducesStructuredJSONAtConfiguredLevel(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger, err := New("warn", &output)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("hidden")
	logger.Warn("visible", "work_item_id", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if strings.Contains(output.String(), "hidden") || !strings.Contains(output.String(), `"level":"WARN"`) {
		t.Fatalf("output=%s", output.String())
	}
}
