package notifications

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestValidateWorkerIDContract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		workerID  string
		want      string
		wantError bool
	}{
		{name: "trimmed 255 ASCII", workerID: "  " + strings.Repeat("a", 255) + "  ", want: strings.Repeat("a", 255)},
		{name: "255 multibyte runes", workerID: strings.Repeat("é", 255), want: strings.Repeat("é", 255)},
		{name: "256 runes", workerID: strings.Repeat("a", 256), wantError: true},
		{name: "blank", workerID: " \t ", wantError: true},
		{name: "NUL", workerID: "worker\x00id", wantError: true},
		{name: "newline", workerID: "worker\nid", wantError: true},
		{name: "trailing newline", workerID: "worker\n", wantError: true},
		{name: "tab", workerID: "worker\tid", wantError: true},
		{name: "delete", workerID: "worker\x7fid", wantError: true},
		{name: "invalid UTF-8", workerID: string([]byte{'w', 0xff, 'x'}), wantError: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateWorkerID(test.workerID)
			if test.wantError {
				if err == nil {
					t.Fatalf("worker ID %q unexpectedly accepted", test.workerID)
				}
				return
			}
			if err != nil || got != test.want || !utf8.ValidString(got) {
				t.Fatalf("got=%q error=%v", got, err)
			}
		})
	}
}
