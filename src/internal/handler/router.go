package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/myronovy/authentication/src/internal/service"
)

func NewRouter(authHandler *AuthHandler, authService service.AuthService) *gin.Engine {
	r := gin.Default()
	r.SetTrustedProxies(nil)

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/google", authHandler.GoogleLogin)
	r.GET("/google/callback", authHandler.GoogleCallback)
	r.POST("/refresh", authHandler.Refresh)
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
