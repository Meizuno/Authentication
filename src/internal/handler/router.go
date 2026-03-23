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

	r.GET("/google", authHandler.GoogleLogin)
	r.GET("/google/callback", authHandler.GoogleCallback)
	r.POST("/refresh", authHandler.Refresh)
	r.GET("/validate", authHandler.Validate)
	r.GET("/me", authHandler.Me)

	return r
}
