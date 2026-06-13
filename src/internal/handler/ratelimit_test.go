package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRateLimiterBlocksOverBurstThenRefills(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rl := NewRateLimiter(1, 2) // 1 rps, burst 2
	clock := time.Unix(1_700_000_000, 0)
	rl.now = func() time.Time { return clock }
	rl.keyFn = func(*gin.Context) string { return "fixed-client" }
	mw := rl.Middleware()

	call := func() int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/refresh", nil)
		mw(c)
		return w.Code
	}

	// Burst of 2 is allowed at the frozen instant...
	if code := call(); code == http.StatusTooManyRequests {
		t.Fatalf("request 1 unexpectedly limited (%d)", code)
	}
	if code := call(); code == http.StatusTooManyRequests {
		t.Fatalf("request 2 unexpectedly limited (%d)", code)
	}
	// ...the third in the same instant is rejected.
	if code := call(); code != http.StatusTooManyRequests {
		t.Fatalf("request 3 = %d, want 429", code)
	}

	// Advancing the clock one second refills exactly one token (1 rps).
	clock = clock.Add(time.Second)
	if code := call(); code == http.StatusTooManyRequests {
		t.Fatalf("request after refill unexpectedly limited (%d)", code)
	}
}

func TestRateLimiterKeysPerClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rl := NewRateLimiter(1, 1) // burst 1: a single request exhausts a client
	clock := time.Unix(1_700_000_000, 0)
	rl.now = func() time.Time { return clock }
	mw := rl.Middleware()

	call := func(ip string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodGet, "/google", nil)
		req.Header.Set("CF-Connecting-IP", ip)
		c.Request = req
		mw(c)
		return w.Code
	}

	if code := call("203.0.113.1"); code == http.StatusTooManyRequests {
		t.Fatalf("client A first request limited (%d)", code)
	}
	if code := call("203.0.113.1"); code != http.StatusTooManyRequests {
		t.Fatalf("client A second request = %d, want 429", code)
	}
	// A different client has its own bucket and is unaffected.
	if code := call("203.0.113.2"); code == http.StatusTooManyRequests {
		t.Fatalf("client B should not be limited by client A's usage (%d)", code)
	}
}

func TestClientKeyPrefersCFConnectingIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("CF-Connecting-IP", "203.0.113.7")

	if got := clientKey(c); got != "203.0.113.7" {
		t.Fatalf("clientKey = %q, want the CF-Connecting-IP value", got)
	}
}
