package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/myronovy/authentication/src/internal/service"
)

const contextUserIDKey = "userID"

// AuthMiddleware parses the Bearer token, validates it, and stores the user id
// in the gin context for downstream handlers. It is the single place access
// tokens are checked, replacing the parsing duplicated across handlers.
func AuthMiddleware(authService service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid authorization header"})
			return
		}

		token := strings.TrimPrefix(header, "Bearer ")
		userID, err := authService.ValidateAccessToken(token)
		if err != nil {
			if errors.Is(err, service.ErrTokenExpired) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token expired"})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		c.Set(contextUserIDKey, userID)
		c.Next()
	}
}

// userIDFromContext returns the user id injected by AuthMiddleware.
func userIDFromContext(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(contextUserIDKey)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok
}
