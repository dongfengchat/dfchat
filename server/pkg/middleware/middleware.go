package middleware

import (
	"net/http"
	"strings"

	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/gin-gonic/gin"
)

func CORS(allowOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(allowOrigins))
	for _, o := range allowOrigins {
		allowed[o] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && allowed[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func RequireAuth(issuer *auth.Issuer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 10001, "message": "missing bearer token"})
			return
		}
		tok := strings.TrimPrefix(h, "Bearer ")
		claims, err := issuer.Parse(tok)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 10002, "message": "invalid token"})
			return
		}
		c.Set("userID", claims.UserID)
		c.Next()
	}
}
