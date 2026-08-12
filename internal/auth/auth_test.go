package auth

import "testing"

func TestHashTokenIsDeterministic(t *testing.T) {
	t.Parallel()
	first := HashToken("dev-manager-token")
	second := HashToken("dev-manager-token")
	if first != second {
		t.Fatal("same token produced different hashes")
	}
	if HashToken("a") == HashToken("b") {
		t.Fatal("different tokens produced the same hash")
	}
}
