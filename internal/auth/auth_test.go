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
	const want = "2fb45d55493f2a01708bf8079a547104e5d60bd027b54ec838971fd0ecbedb54"
	if got := HashToken("dev-manager-token"); got != want {
		t.Fatalf("HashToken()=%q want SHA-256 %q", got, want)
	}
}

func TestAuthenticateHashesTokenAndMapsStoreFailures(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 30, 0, 0, time.UTC)
	const token = "dev-manager-token"
	const wantHash = "2fb45d55493f2a01708bf8079a547104e5d60bd027b54ec838971fd0ecbedb54"
	tests := []struct {
		name      string
		token     string
		principal Principal
		storeErr  error
		wantErr   error
		wantStore bool
	}{
		{name: "blank token", token: " \t", wantErr: ErrAuthenticationRequired},
		{name: "not found", token: token, storeErr: ErrPrincipalNotFound, wantErr: ErrInvalidCredentials, wantStore: true},
		{name: "store failure", token: token, storeErr: errors.New("database unavailable"), wantErr: ErrAuthenticationUnavailable, wantStore: true},
		{name: "missing principal id", token: token, principal: Principal{Role: RoleManager}, wantErr: ErrInvalidCredentials, wantStore: true},
		{name: "invalid principal role", token: token, principal: Principal{ID: "user", Role: Role("ADMIN")}, wantErr: ErrInvalidCredentials, wantStore: true},
		{name: "valid", token: token, principal: Principal{ID: "user", Role: RoleWorker}, wantStore: true},
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
			if test.wantStore && (store.hash != wantHash || !store.now.Equal(now)) {
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
