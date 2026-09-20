package handler

import (
	"errors"
	"net/http"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"

	"github.com/gin-gonic/gin"
)

type OAuthHandler struct {
	service service.OAuthService
}

func NewOAuthHandler(service service.OAuthService) *OAuthHandler {
	return &OAuthHandler{service: service}
}

type googleLoginRequest struct {
	IDToken string `json:"id_token" binding:"required"`
}

// LoginWithGoogle implementa la US de login federado: CA1 (cuenta nueva),
// CA2 (vincula a una cuenta existente por email) y CA3 (si Google no
// responde, devuelve 503 para que el cliente ofrezca el login con
// email/contrasena como alternativa).
func (h *OAuthHandler) LoginWithGoogle(c *gin.Context) {
	var req googleLoginRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.APIError{
				Code:    "INVALID_INPUT",
				Message: "Falta el id_token del proveedor externo",
				Details: map[string]any{"reason": err.Error()},
			},
		})
		return
	}

	res, err := h.service.LoginWithGoogle(c.Request.Context(), req.IDToken)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOAuthProviderUnavailable):
			c.JSON(http.StatusServiceUnavailable, model.ErrorResponse{
				Error: model.APIError{
					Code:    "OAUTH_PROVIDER_UNAVAILABLE",
					Message: "No pudimos contactar al proveedor de identidad externo. Inicia sesión con tu correo y contraseña.",
				},
			})

		case errors.Is(err, service.ErrOAuthTokenInvalid):
			c.JSON(http.StatusUnauthorized, model.ErrorResponse{
				Error: model.APIError{
					Code:    "OAUTH_TOKEN_INVALID",
					Message: "No pudimos validar tu identidad con el proveedor externo",
				},
			})

		default:
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{
				Error: model.APIError{
					Code:    "INTERNAL_SERVER_ERROR",
					Message: "Ha ocurrido un error inesperado en el servidor",
				},
			})
		}
		return
	}

	setRefreshCookie(c, res.RefreshToken)
	res.RefreshToken = ""
	c.JSON(http.StatusOK, res)
}
