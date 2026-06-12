package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuthMiddlewareRejectsMissingBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mw := AuthMiddleware(&mockAuthService{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/me", nil)

	mw(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing Bearer: got status %d, want 401", w.Code)
	}
	if !c.IsAborted() {
		t.Fatal("expected the chain to be aborted")
	}
}

func TestAuthMiddlewareInjectsUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mw := AuthMiddleware(&mockAuthService{}) // ValidateAccessToken returns uuid.Nil, nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer good-token")
	c.Request = req

	mw(c)

	if c.IsAborted() {
		t.Fatalf("valid token was rejected with status %d", w.Code)
	}
	if _, ok := userIDFromContext(c); !ok {
		t.Fatal("user id was not injected into the context")
	}
}
