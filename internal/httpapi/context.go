package httpapi

import (
	"context"
	"github.com/eidrogo/work-management-api/internal/auth"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	principalKey contextKey = "principal"
)

func withRequestID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, requestIDKey, value)
}
func requestIDFrom(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}
func withPrincipal(ctx context.Context, value auth.Principal) context.Context {
	return context.WithValue(ctx, principalKey, value)
}
func principalFrom(ctx context.Context) (auth.Principal, bool) {
	value, ok := ctx.Value(principalKey).(auth.Principal)
	return value, ok
}
