package postgres

import (
	"database/sql/driver"
	"testing"
	"time"
)

func TestEncodeDecodeParameters(t *testing.T) {
	t.Parallel()
	encoded, null, err := encodeParameter(int64(42))
	if err != nil || null || encoded != "42" {
		t.Fatalf("encoded=%q null=%v err=%v", encoded, null, err)
	}
	decoded, err := decodeValue(23, "42")
	if err != nil || decoded != driver.Value(int64(42)) {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	value, err := decodeValue(1184, "2026-08-12 12:00:00+00")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := value.(time.Time); !ok {
		t.Fatalf("timestamp type=%T", value)
	}
}
