package handler

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/myronovy/authentication/src/internal/config"
	"github.com/myronovy/authentication/src/internal/domain"
	"github.com/myronovy/authentication/src/internal/service"
)

const (
	oauthStateCookie  = "oauth_state"
	redirectURLCookie = "auth_redirect_url"
	oauthCookieMaxAge = 300 // seconds; the OAuth round-trip is short-lived

	accessTokenCookie   = "access_token"
	refreshTokenCookie  = "refresh_token"
	accessCookieMaxAge  = 15 * 60          // mirrors the access-token TTL
	refreshCookieMaxAge = 7 * 24 * 60 * 60 // mirrors the refresh-token TTL
)

type AuthHandler struct {
	authService service.AuthService
	cfg         *config.Config
}

func NewAuthHandler(authService service.AuthService, cfg *config.Config) *AuthHandler {
	return &AuthHandler{authService: authService, cfg: cfg}
}

// setCookie writes a hardened cookie. Secure is hardcoded here; item 6 makes it
// configurable so non-HTTPS local dev can still receive cookies.
func (h *AuthHandler) setCookie(c *gin.Context, name, value string, maxAge int, httpOnly bool) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, value, maxAge, "/", "", true, httpOnly)
}

func (h *AuthHandler) clearCookie(c *gin.Context, name string) {
	h.setCookie(c, name, "", -1, true)
}

// setTokenCookies delivers the token pair to the browser. The refresh token is
// httpOnly so client JS can never read it; the short-lived access token is
// readable so SPAs can attach it as a Bearer header.
func (h *AuthHandler) setTokenCookies(c *gin.Context, pair *domain.TokenPair) {
	h.setCookie(c, refreshTokenCookie, pair.RefreshToken, refreshCookieMaxAge, true)
	h.setCookie(c, accessTokenCookie, pair.AccessToken, accessCookieMaxAge, false)
}

// isAllowedRedirect reports whether url exactly matches a configured target.
// Exact matching closes the open-redirect: an attacker cannot point the flow at
// their own domain to harvest tokens.
func (h *AuthHandler) isAllowedRedirect(url string) bool {
	for _, allowed := range h.cfg.AllowedRedirectURLs {
		if url == allowed {
			return true
		}
	}
	return false
}

func (h *AuthHandler) GoogleLogin(c *gin.Context) {
	state, err := generateState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state"})
		return
	}

	// Bind the state to the browser so the callback can prove this request
	// originated from us (CSRF defense).
	h.setCookie(c, oauthStateCookie, state, oauthCookieMaxAge, true)

	if redirectURL := c.Query("redirect_url"); redirectURL != "" {
		h.setCookie(c, redirectURLCookie, redirectURL, oauthCookieMaxAge, true)
	}

	url := h.authService.GetGoogleAuthURL(state)
	c.Redirect(http.StatusTemporaryRedirect, url)
}

func (h *AuthHandler) GoogleCallback(c *gin.Context) {
	if !h.verifyState(c) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid oauth state"})
		return
	}

	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing code"})
		return
	}

	pair, err := h.authService.HandleGoogleCallback(c.Request.Context(), code)
	if err != nil {
		if errors.Is(err, service.ErrEmailNotAllowed) {
			c.JSON(http.StatusForbidden, gin.H{"error": "email not allowed"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "authentication failed"})
		return
	}

	// Tokens are delivered via cookies only — never appended to a URL where
	// they would leak through browser history, Referer, and proxy logs.
	h.setTokenCookies(c, pair)

	if redirectURL, err := c.Cookie(redirectURLCookie); err == nil && redirectURL != "" {
		h.clearCookie(c, redirectURLCookie)
		if !h.isAllowedRedirect(redirectURL) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "redirect_url not allowed"})
			return
		}
		// No tokens in the query string; the browser already holds the cookies.
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token": pair.AccessToken,
	})
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	refreshToken := h.readRefreshToken(c)
	if refreshToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing refresh token"})
		return
	}

	pair, err := h.authService.RefreshTokens(c.Request.Context(), refreshToken)
	if err != nil {
		if errors.Is(err, service.ErrInvalidToken) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired refresh token"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to refresh tokens"})
		return
	}

	// Rotated refresh token rides back in the httpOnly cookie; only the access
	// token is exposed in the body.
	h.setTokenCookies(c, pair)
	c.JSON(http.StatusOK, gin.H{
		"access_token": pair.AccessToken,
	})
}

// readRefreshToken prefers the httpOnly cookie and falls back to a JSON body for
// non-browser clients.
func (h *AuthHandler) readRefreshToken(c *gin.Context) string {
	if cookie, err := c.Cookie(refreshTokenCookie); err == nil && cookie != "" {
		return cookie
	}
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = c.ShouldBindJSON(&body)
	return body.RefreshToken
}

func (h *AuthHandler) Validate(c *gin.Context) {
	token := c.GetHeader("Authorization")
	if len(token) < 8 || token[:7] != "Bearer " {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid authorization header"})
		return
	}

	userID, err := h.authService.ValidateAccessToken(token[7:])
	if err != nil {
		if errors.Is(err, service.ErrTokenExpired) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "token expired"})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"user_id": userID})
}

func (h *AuthHandler) Me(c *gin.Context) {
	token := c.GetHeader("Authorization")
	if len(token) < 8 || token[:7] != "Bearer " {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid authorization header"})
		return
	}

	userID, err := h.authService.ValidateAccessToken(token[7:])
	if err != nil {
		if errors.Is(err, service.ErrTokenExpired) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "token expired"})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}

	user, err := h.authService.GetMe(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":         user.ID,
		"email":      user.Email,
		"name":       user.Name,
		"avatar_url": user.AvatarURL,
	})
}

// verifyState confirms the state query param matches the value stored in the
// httpOnly cookie set at /google. The cookie is always cleared afterwards so a
// state cannot be replayed. Returns false on any absence or mismatch.
func (h *AuthHandler) verifyState(c *gin.Context) bool {
	state := c.Query("state")
	cookie, err := c.Cookie(oauthStateCookie)
	h.clearCookie(c, oauthStateCookie)

	if state == "" || err != nil || cookie == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(state), []byte(cookie)) == 1
}

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
