package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/myronovy/authentication/src/internal/config"
	"github.com/myronovy/authentication/src/internal/domain"
)

// mockAuthService is a configurable stand-in for service.AuthService.
type mockAuthService struct {
	callbackPair *domain.TokenPair
	callbackErr  error
}

func (m *mockAuthService) GetGoogleAuthURL(state string) string {
	return "https://accounts.google.com/o/oauth2?state=" + state
}

func (m *mockAuthService) HandleGoogleCallback(_ context.Context, _ string) (*domain.TokenPair, error) {
	return m.callbackPair, m.callbackErr
}

func (m *mockAuthService) RefreshTokens(_ context.Context, _ string) (*domain.TokenPair, error) {
	return m.callbackPair, m.callbackErr
}

func (m *mockAuthService) ValidateAccessToken(_ string) (uuid.UUID, error) { return uuid.Nil, nil }

func (m *mockAuthService) GetMe(_ context.Context, _ uuid.UUID) (*domain.User, error) {
	return nil, nil
}

func newTestHandler(svc *mockAuthService, cfg *config.Config) *AuthHandler {
	gin.SetMode(gin.TestMode)
	if cfg == nil {
		cfg = &config.Config{}
	}
	return NewAuthHandler(svc, cfg)
}

func TestGoogleCallbackRejectsMissingState(t *testing.T) {
	h := newTestHandler(&mockAuthService{callbackPair: &domain.TokenPair{}}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// No state query param, no state cookie.
	c.Request = httptest.NewRequest(http.MethodGet, "/google/callback?code=abc", nil)

	h.GoogleCallback(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing state: got status %d, want 400", w.Code)
	}
}

func TestGoogleCallbackRejectsMismatchedState(t *testing.T) {
	h := newTestHandler(&mockAuthService{callbackPair: &domain.TokenPair{}}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/google/callback?code=abc&state=attacker", nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: "real-state"})
	c.Request = req

	h.GoogleCallback(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("mismatched state: got status %d, want 400", w.Code)
	}
}

func TestGoogleCallbackAcceptsMatchingState(t *testing.T) {
	h := newTestHandler(&mockAuthService{callbackPair: &domain.TokenPair{AccessToken: "a", RefreshToken: "r"}}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/google/callback?code=abc&state=match", nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: "match"})
	c.Request = req

	h.GoogleCallback(c)

	if w.Code == http.StatusBadRequest {
		t.Fatalf("matching state was rejected with 400")
	}
}
