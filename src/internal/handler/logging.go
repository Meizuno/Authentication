package handler

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/myronovy/authentication/src/internal/audit"
)

// RequestLogger logs one structured slog record per request, replacing gin's
// default text logger.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.LogAttrs(c.Request.Context(), slog.LevelInfo, "request",
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
			slog.String("ip", clientKey(c)),
		)
	}
}

// ClientIPContext stashes the real client IP into the request context so audit
// events emitted by deeper layers can include it.
func ClientIPContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := audit.ContextWithClientIP(c.Request.Context(), clientKey(c))
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
