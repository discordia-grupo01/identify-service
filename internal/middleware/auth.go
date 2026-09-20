package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	UserIDKey = "auth_user_id"
)

func RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authorization := c.GetHeader("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") {
			unauthorized(c)
			return
		}

		claims := jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(strings.TrimPrefix(authorization, "Bearer "), claims, func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, jwt.ErrTokenSignatureInvalid
			}
			return []byte(os.Getenv("JWT_SECRET")), nil
		})
		if err != nil || !token.Valid {
			unauthorized(c)
			return
		}

		userID, ok := claims["sub"].(string)
		if !ok || userID == "" {
			unauthorized(c)
			return
		}

		c.Set(UserIDKey, userID)
		c.Next()
	}
}

func unauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, model.ErrorResponse{
		Error: model.APIError{
			Code:    "SESSION_EXPIRED",
			Message: "La sesión expiró o ya no es válida",
			Details: map[string]any{
				"redirect_to": "/v1/login",
				"pending_action": map[string]string{
					"method": c.Request.Method,
					"path":   c.Request.URL.RequestURI(),
				},
			},
		},
	})
}
