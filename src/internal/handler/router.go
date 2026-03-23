package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func NewRouter(authHandler *AuthHandler) *gin.Engine {
	r := gin.Default()
	r.SetTrustedProxies(nil)

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	auth := r.Group("/auth")
	{
		auth.GET("/google", authHandler.GoogleLogin)
		auth.GET("/google/callback", authHandler.GoogleCallback)
		auth.POST("/refresh", authHandler.Refresh)
		auth.GET("/validate", authHandler.Validate)
		auth.GET("/me", authHandler.Me)
	}

	return r
}
