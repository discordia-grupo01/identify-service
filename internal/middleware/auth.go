package middleware

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	UserIDKey = "auth_user_id"
)

// SuspensionChecker dice si una cuenta está suspendida.
type SuspensionChecker interface {
	IsSuspended(ctx context.Context, userID string) (bool, error)
}

// RequireAuth valida el JWT y, si recibe un checker, rechaza con
// 403 ACCOUNT_SUSPENDED a un usuario suspendido aunque su access token siga
// vigente (el JWT es stateless y no se puede revocar). Con checker nil no
// consulta el estado de la cuenta.
func RequireAuth(checker SuspensionChecker) gin.HandlerFunc {
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

		if checker != nil {
			suspended, err := checker.IsSuspended(c.Request.Context(), userID)
			if err != nil {
				// Fallar cerrado: sin poder saber si la cuenta está suspendida
				// no se deja pasar la request.
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, model.ErrorResponse{
					Error: model.APIError{
						Code:    "SERVICE_UNAVAILABLE",
						Message: "No pudimos verificar el estado de la cuenta, probá de nuevo en unos segundos",
					},
				})
				return
			}
			if suspended {
				c.AbortWithStatusJSON(http.StatusForbidden, model.ErrorResponse{
					Error: model.APIError{
						Code:    "ACCOUNT_SUSPENDED",
						Message: "Tu cuenta está suspendida",
					},
				})
				return
			}
		}

		c.Set(UserIDKey, userID)
		c.Next()
	}
}

// maxSuspensionCacheEntries acota la caché: al superarlo se descartan las
// entradas vencidas antes de guardar una nueva.
const maxSuspensionCacheEntries = 10000

// SuspensionCache es un SuspensionChecker con una caché en memoria de pocos
// segundos delante de otro, para no ir a la base en cada request. La ventana
// de inconsistencia es el TTL, salvo en esta instancia: Invalidate la corta en
// el momento en que se aplica un evento de suspensión. Los errores no se
// cachean.
type SuspensionCache struct {
	checker SuspensionChecker
	ttl     time.Duration
	now     func() time.Time

	mu      sync.Mutex
	entries map[string]suspensionEntry
}

type suspensionEntry struct {
	suspended bool
	expiresAt time.Time
}

func NewSuspensionCache(checker SuspensionChecker, ttl time.Duration) *SuspensionCache {
	return &SuspensionCache{checker: checker, ttl: ttl, now: time.Now, entries: map[string]suspensionEntry{}}
}

func (c *SuspensionCache) IsSuspended(ctx context.Context, userID string) (bool, error) {
	now := c.now()
	c.mu.Lock()
	entry, ok := c.entries[userID]
	c.mu.Unlock()
	if ok && now.Before(entry.expiresAt) {
		return entry.suspended, nil
	}

	suspended, err := c.checker.IsSuspended(ctx, userID)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	if len(c.entries) >= maxSuspensionCacheEntries {
		for id, e := range c.entries {
			if !now.Before(e.expiresAt) {
				delete(c.entries, id)
			}
		}
	}
	c.entries[userID] = suspensionEntry{suspended: suspended, expiresAt: now.Add(c.ttl)}
	c.mu.Unlock()
	return suspended, nil
}

// Invalidate descarta lo cacheado de userID: la próxima request consulta la base.
func (c *SuspensionCache) Invalidate(userID string) {
	c.mu.Lock()
	delete(c.entries, userID)
	c.mu.Unlock()
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
