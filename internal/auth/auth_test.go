package auth

import "testing"

func TestHashTokenIsDeterministic(t *testing.T) {
	t.Parallel()
	if HashToken("dev-manager-token") != HashToken("dev-manager-token") || HashToken("a") == HashToken("b") {
		t.Fatal("unexpected token hash behavior")
	}
}
