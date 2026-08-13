package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/testsupport/postgrestest"
)

func TestPostgresTokenStoreHonorsExpiry(t *testing.T) {
	db := postgrestest.Open(t)
	now := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)
	postgrestest.MustExec(t, db, `INSERT INTO users(id,display_name,email,role,created_at) VALUES
('11111111-1111-4111-8111-111111111111','Active','active@example.test','MANAGER',$1),
('22222222-2222-4222-8222-222222222222','Expired','expired@example.test','WORKER',$1),
('33333333-3333-4333-8333-333333333333','Equal','equal@example.test','WORKER',$1),
('44444444-4444-4444-8444-444444444444','No Expiry','no-expiry@example.test','WORKER',$1)`, now)
	postgrestest.MustExec(t, db, `INSERT INTO auth_tokens(token_hash,user_id,created_at,expires_at) VALUES
($1,'11111111-1111-4111-8111-111111111111',$3::timestamptz,$3::timestamptz + interval '1 hour'),
($2,'22222222-2222-4222-8222-222222222222',$3::timestamptz,$3::timestamptz - interval '1 second'),
($4,'33333333-3333-4333-8333-333333333333',$3::timestamptz,$3::timestamptz),
($5,'44444444-4444-4444-8444-444444444444',$3::timestamptz,NULL)`, HashToken("active-token"), HashToken("expired-token"), now, HashToken("equal-token"), HashToken("no-expiry-token"))
	store := PostgresTokenStore{DB: db}
	principal, err := store.ResolvePrincipal(context.Background(), HashToken("active-token"), now)
	if err != nil || principal.ID != "11111111-1111-4111-8111-111111111111" || principal.Role != RoleManager {
		t.Fatalf("active principal=%#v error=%v", principal, err)
	}
	principal, err = store.ResolvePrincipal(context.Background(), HashToken("no-expiry-token"), now)
	if err != nil || principal.ID != "44444444-4444-4444-8444-444444444444" {
		t.Fatalf("no-expiry principal=%#v error=%v", principal, err)
	}
	for _, token := range []string{"expired-token", "equal-token", "unknown-token"} {
		if _, err = store.ResolvePrincipal(context.Background(), HashToken(token), now); !errors.Is(err, ErrPrincipalNotFound) {
			t.Fatalf("token=%s error=%v", token, err)
		}
	}
}
