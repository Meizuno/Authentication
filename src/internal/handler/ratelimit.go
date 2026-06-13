package handler

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimiter is a per-client token-bucket limiter for the unauthenticated auth
// endpoints. State is in-memory and therefore per-instance — adequate for a
// single replica; a multi-replica deployment would need a shared store.
type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]*rate.Limiter
	rps     rate.Limit
	burst   int

	// keyFn and now are injectable for testing (fake key / fake clock).
	keyFn func(*gin.Context) string
	now   func() time.Time
}

func NewRateLimiter(rps float64, burst int) *RateLimiter {
	return &RateLimiter{
		clients: make(map[string]*rate.Limiter),
		rps:     rate.Limit(rps),
		burst:   burst,
		keyFn:   clientKey,
		now:     time.Now,
	}
}

func (rl *RateLimiter) limiterFor(key string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	l, ok := rl.clients[key]
	if !ok {
		l = rate.NewLimiter(rl.rps, rl.burst)
		rl.clients[key] = l
	}
	return l
}

// Middleware rejects requests from a client that exceeds its bucket with 429.
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rl.limiterFor(rl.keyFn(c)).AllowN(rl.now(), 1) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}

// clientKey identifies the real client. Behind Cloudflare with
// SetTrustedProxies(nil), gin's ClientIP() resolves to the proxy, not the user,
// so prefer Cloudflare's CF-Connecting-IP header and fall back to ClientIP.
func clientKey(c *gin.Context) string {
	if ip := c.GetHeader("CF-Connecting-IP"); ip != "" {
		return ip
	}
	return c.ClientIP()
}
