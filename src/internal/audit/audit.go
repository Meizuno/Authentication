// Package audit emits structured security-audit events via log/slog. It never
// records secrets: callers must pass identifiers (user id, email, family id),
// never tokens or token hashes.
package audit

import (
	"context"
	"log/slog"
)

type ctxKey int

const clientIPKey ctxKey = iota

// ContextWithClientIP stores the real client IP so audit events emitted deeper
// in the stack (e.g. the service layer) can include it.
func ContextWithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey, ip)
}

func clientIP(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey).(string)
	return ip
}

// Event records an audit event with a stable {event, outcome, ip, ...} shape.
func Event(ctx context.Context, event, outcome string, attrs ...slog.Attr) {
	all := make([]slog.Attr, 0, len(attrs)+3)
	all = append(all, slog.String("event", event), slog.String("outcome", outcome))
	if ip := clientIP(ctx); ip != "" {
		all = append(all, slog.String("ip", ip))
	}
	all = append(all, attrs...)
	slog.LogAttrs(ctx, slog.LevelInfo, "audit", all...)
}
