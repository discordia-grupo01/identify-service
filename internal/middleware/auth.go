package middleware

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	UserIDKey  = "auth_user_id"
	TokenIDKey = "auth_token_id"
)

func RequireAuth(sessionRepo repository.SessionRepository) gin.HandlerFunc {
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

		tokenID, ok := claims["jti"].(string)
		if !ok || tokenID == "" {
			unauthorized(c)
			return
		}
		userID, err := claimInt64(claims["sub"])
		if err != nil {
			unauthorized(c)
			return
		}

		active, err := sessionRepo.IsActive(c.Request.Context(), tokenID)
		if err != nil || !active {
			unauthorized(c)
			return
		}

		c.Set(UserIDKey, userID)
		c.Set(TokenIDKey, tokenID)
		c.Next()
	}
}

func claimInt64(value any) (int64, error) {
	switch number := value.(type) {
	case float64:
		return int64(number), nil
	case string:
		return strconv.ParseInt(number, 10, 64)
	default:
		return 0, strconv.ErrSyntax
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
