package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/myronovy/authentication/src/internal/config"
	"github.com/myronovy/authentication/src/internal/domain"
)

// mockAuthService is a configurable stand-in for service.AuthService.
type mockAuthService struct {
	callbackPair    *domain.TokenPair
	callbackErr     error
	loggedOutToken  string
	loggedOutUserID uuid.UUID
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

func (m *mockAuthService) Logout(_ context.Context, refreshToken string) error {
	m.loggedOutToken = refreshToken
	return nil
}

func (m *mockAuthService) LogoutAll(_ context.Context, userID uuid.UUID) error {
	m.loggedOutUserID = userID
	return nil
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

// callbackWithState drives GoogleCallback through a valid state check and the
// given redirect_url cookie, returning the recorder.
func callbackWithState(h *AuthHandler, redirectURL string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/google/callback?code=abc&state=match", nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: "match"})
	if redirectURL != "" {
		req.AddCookie(&http.Cookie{Name: redirectURLCookie, Value: redirectURL})
	}
	c.Request = req
	h.GoogleCallback(c)
	return w
}

func TestGoogleCallbackRejectsNonAllowlistedRedirect(t *testing.T) {
	cfg := &config.Config{AllowedRedirectURLs: []string{"https://app.example.com/auth"}}
	h := newTestHandler(&mockAuthService{callbackPair: &domain.TokenPair{AccessToken: "a", RefreshToken: "r"}}, cfg)

	w := callbackWithState(h, "https://evil.example.com/harvest")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-allowlisted redirect: got status %d, want 400", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("expected no redirect, got Location %q", loc)
	}
}

func TestGoogleCallbackAllowlistedRedirectCarriesNoTokens(t *testing.T) {
	const target = "https://app.example.com/auth"
	cfg := &config.Config{AllowedRedirectURLs: []string{target}}
	h := newTestHandler(&mockAuthService{callbackPair: &domain.TokenPair{AccessToken: "the-access", RefreshToken: "the-refresh"}}, cfg)

	w := callbackWithState(h, target)

	loc := w.Header().Get("Location")
	if loc != target {
		t.Fatalf("Location = %q, want exactly %q (no query)", loc, target)
	}
	if strings.Contains(loc, "access_token") || strings.Contains(loc, "refresh_token") ||
		strings.Contains(loc, "the-access") || strings.Contains(loc, "the-refresh") {
		t.Fatalf("token leaked into redirect Location: %q", loc)
	}

	// Refresh token must be delivered as an httpOnly cookie, never the body.
	var sawRefreshCookie bool
	for _, ck := range w.Result().Cookies() {
		if ck.Name == refreshTokenCookie {
			sawRefreshCookie = true
			if !ck.HttpOnly {
				t.Fatal("refresh_token cookie is not httpOnly")
			}
		}
	}
	if !sawRefreshCookie {
		t.Fatal("refresh_token cookie was not set")
	}
}

func TestCookiesAreSecureAndSameSite(t *testing.T) {
	cfg := &config.Config{CookieSecure: true}
	h := newTestHandler(&mockAuthService{}, cfg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/google", nil)

	h.GoogleLogin(c)

	var found bool
	for _, ck := range w.Result().Cookies() {
		if ck.Name != oauthStateCookie {
			continue
		}
		found = true
		if !ck.Secure {
			t.Error("oauth_state cookie is not Secure")
		}
		if ck.SameSite != http.SameSiteLaxMode {
			t.Errorf("oauth_state SameSite = %v, want Lax", ck.SameSite)
		}
		if !ck.HttpOnly {
			t.Error("oauth_state cookie is not HttpOnly")
		}
	}
	if !found {
		t.Fatal("oauth_state cookie was not set")
	}
}

func TestLogoutRevokesPresentedTokenAndClearsCookies(t *testing.T) {
	mock := &mockAuthService{}
	h := newTestHandler(mock, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: refreshTokenCookie, Value: "rtoken"})
	c.Request = req

	h.Logout(c)

	if w.Code != http.StatusOK {
		t.Fatalf("logout: got status %d, want 200", w.Code)
	}
	if mock.loggedOutToken != "rtoken" {
		t.Fatalf("service.Logout got %q, want %q", mock.loggedOutToken, "rtoken")
	}
	// The refresh cookie must be cleared (MaxAge < 0).
	var cleared bool
	for _, ck := range w.Result().Cookies() {
		if ck.Name == refreshTokenCookie && ck.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("refresh_token cookie was not cleared on logout")
	}
}

func TestRefreshReadsTokenFromCookie(t *testing.T) {
	h := newTestHandler(&mockAuthService{callbackPair: &domain.TokenPair{AccessToken: "a2", RefreshToken: "r2"}}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: refreshTokenCookie, Value: "r1"})
	c.Request = req

	h.Refresh(c)

	if w.Code != http.StatusOK {
		t.Fatalf("refresh via cookie: got status %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), "refresh_token") {
		t.Fatalf("refresh token leaked into response body: %s", w.Body.String())
	}
}
