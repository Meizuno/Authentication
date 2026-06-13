package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/myronovy/authentication/src/internal/config"
	"github.com/myronovy/authentication/src/internal/service"
)

func NewRouter(authHandler *AuthHandler, authService service.AuthService, cfg *config.Config) *gin.Engine {
	// gin.New (not Default) so we use the structured slog request logger instead
	// of gin's text logger.
	r := gin.New()
	r.Use(gin.Recovery(), RequestLogger(), ClientIPContext())
	// nil clears the trusted-proxy list (we sit behind Cloudflare and read
	// CF-Connecting-IP); this never errors for a nil argument.
	_ = r.SetTrustedProxies(nil)

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Public key set for local token verification by consumers.
	r.GET("/.well-known/jwks.json", authHandler.JWKS)

	// Per-client rate limiting on the unauthenticated endpoints.
	limit := NewRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst).Middleware()
	r.GET("/google", limit, authHandler.GoogleLogin)
	r.GET("/google/callback", limit, authHandler.GoogleCallback)
	r.POST("/refresh", limit, authHandler.Refresh)
	r.POST("/logout", authHandler.Logout)

	// Routes that require a valid access token.
	protected := r.Group("/")
	protected.Use(AuthMiddleware(authService))
	{
		protected.POST("/logout-all", authHandler.LogoutAll)
		protected.GET("/validate", authHandler.Validate)
		protected.GET("/me", authHandler.Me)
	}

	return r
}
