package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/platform/clock"
)

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

func TestAuthenticateHashesTokenAndMapsStoreFailures(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 30, 0, 0, time.UTC)
	tests := []struct {
		name      string
		token     string
		principal Principal
		storeErr  error
		wantErr   error
		wantStore bool
	}{
		{name: "blank token", token: " \t", wantErr: ErrAuthenticationRequired},
		{name: "not found", token: "missing", storeErr: ErrPrincipalNotFound, wantErr: ErrInvalidCredentials, wantStore: true},
		{name: "store failure", token: "valid", storeErr: errors.New("database unavailable"), wantErr: ErrAuthenticationUnavailable, wantStore: true},
		{name: "missing principal id", token: "valid", principal: Principal{Role: RoleManager}, wantErr: ErrInvalidCredentials, wantStore: true},
		{name: "invalid principal role", token: "valid", principal: Principal{ID: "user", Role: Role("ADMIN")}, wantErr: ErrInvalidCredentials, wantStore: true},
		{name: "valid", token: "valid", principal: Principal{ID: "user", Role: RoleWorker}, wantStore: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &capturingTokenStore{principal: test.principal, err: test.storeErr}
			service := NewService(store, clock.Fixed{Time: now})
			principal, err := service.Authenticate(context.Background(), test.token)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v want=%v", err, test.wantErr)
			}
			if store.called != test.wantStore {
				t.Fatalf("store called=%v want=%v", store.called, test.wantStore)
			}
			if test.wantStore && (store.hash != HashToken(test.token) || !store.now.Equal(now)) {
				t.Fatalf("hash=%q now=%s", store.hash, store.now)
			}
			if test.wantErr == nil && principal != test.principal {
				t.Fatalf("principal=%#v", principal)
			}
		})
	}
}

type capturingTokenStore struct {
	principal Principal
	err       error
	called    bool
	hash      string
	now       time.Time
}

func (store *capturingTokenStore) ResolvePrincipal(_ context.Context, hash string, now time.Time) (Principal, error) {
	store.called = true
	store.hash = hash
	store.now = now
	return store.principal, store.err
}
