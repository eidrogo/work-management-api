package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eidrogo/work-management-api/internal/platform/clock"
)

type Role string

const (
	RoleManager Role = "MANAGER"
	RoleWorker  Role = "WORKER"
)

type Principal struct {
	ID          string
	DisplayName string
	Email       string
	Role        Role
}

type User = Principal

var (
	ErrAuthenticationRequired    = errors.New("authentication required")
	ErrInvalidCredentials        = errors.New("invalid credentials")
	ErrAuthenticationUnavailable = errors.New("authentication unavailable")
	ErrPrincipalNotFound         = errors.New("principal not found")
)

type TokenStore interface {
	ResolvePrincipal(context.Context, string, time.Time) (Principal, error)
}

type Service struct {
	store TokenStore
	clock clock.Clock
}

func NewService(store TokenStore, current clock.Clock) *Service {
	return &Service{store: store, clock: current}
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (service *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if strings.TrimSpace(token) == "" {
		return Principal{}, ErrAuthenticationRequired
	}
	if service == nil || service.store == nil || service.clock == nil {
		return Principal{}, ErrAuthenticationUnavailable
	}
	principal, err := service.store.ResolvePrincipal(ctx, HashToken(token), service.clock.Now())
	if errors.Is(err, ErrPrincipalNotFound) {
		return Principal{}, ErrInvalidCredentials
	}
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrAuthenticationUnavailable, err)
	}
	if principal.ID == "" || (principal.Role != RoleManager && principal.Role != RoleWorker) {
		return Principal{}, ErrInvalidCredentials
	}
	return principal, nil
}

type PostgresTokenStore struct{ DB *sql.DB }

func (store PostgresTokenStore) ResolvePrincipal(ctx context.Context, hash string, now time.Time) (Principal, error) {
	var principal Principal
	var role string
	err := store.DB.QueryRowContext(ctx, `
SELECT users.id::text, users.display_name, users.email, users.role
FROM auth_tokens JOIN users ON users.id = auth_tokens.user_id
WHERE auth_tokens.token_hash = $1
  AND (auth_tokens.expires_at IS NULL OR auth_tokens.expires_at > $2)`, hash, now.UTC()).
		Scan(&principal.ID, &principal.DisplayName, &principal.Email, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrPrincipalNotFound
	}
	if err != nil {
		return Principal{}, fmt.Errorf("resolve principal: %w", err)
	}
	principal.Role = Role(role)
	return principal, nil
}
