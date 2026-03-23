package handler

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/myronovy/authentication/src/internal/service"
)

type AuthHandler struct {
	authService service.AuthService
}

func NewAuthHandler(authService service.AuthService) *AuthHandler {
	return &AuthHandler{authService: authService}
}

func (h *AuthHandler) GoogleLogin(c *gin.Context) {
	state, err := generateState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state"})
		return
	}

	if redirectURL := c.Query("redirect_url"); redirectURL != "" {
		c.SetCookie("auth_redirect_url", redirectURL, 300, "/", "", false, true)
	}

	url := h.authService.GetGoogleAuthURL(state)
	c.Redirect(http.StatusTemporaryRedirect, url)
}

func (h *AuthHandler) GoogleCallback(c *gin.Context) {
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

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
