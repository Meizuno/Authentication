package handler

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/myronovy/authentication/src/internal/config"
	"github.com/myronovy/authentication/src/internal/service"
)

const (
	oauthStateCookie  = "oauth_state"
	redirectURLCookie = "auth_redirect_url"
	oauthCookieMaxAge = 300 // seconds; the OAuth round-trip is short-lived
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

	if redirectURL, err := c.Cookie("auth_redirect_url"); err == nil && redirectURL != "" {
		c.SetCookie("auth_redirect_url", "", -1, "/", "", false, true)
		c.Redirect(http.StatusTemporaryRedirect,
			redirectURL+"?access_token="+pair.AccessToken+"&refresh_token="+pair.RefreshToken)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  pair.AccessToken,
		"refresh_token": pair.RefreshToken,
	})
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	var body struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pair, err := h.authService.RefreshTokens(c.Request.Context(), body.RefreshToken)
	if err != nil {
		if errors.Is(err, service.ErrInvalidToken) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired refresh token"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to refresh tokens"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  pair.AccessToken,
		"refresh_token": pair.RefreshToken,
	})
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
