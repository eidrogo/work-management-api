package id

import "testing"

func TestNewUUID(t *testing.T) {
	t.Parallel()
	first, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	if !IsUUID(first) || first[14] != '4' {
		t.Fatalf("invalid v4 UUID %q", first)
	}
	if first == second {
		t.Fatalf("duplicate UUID %q", first)
	}
}
