package workitems

import (
	"testing"
	"time"
)

func TestListCursorRoundTrip(t *testing.T) {
	t.Parallel()
	original := ListCursor{CreatedAt: time.Date(2026, 8, 12, 15, 30, 45, 0, time.UTC), ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}
	encoded, err := EncodeListCursor(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeListCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.CreatedAt.Equal(original.CreatedAt) || decoded.ID != original.ID {
		t.Fatalf("got=%#v", decoded)
	}
}
