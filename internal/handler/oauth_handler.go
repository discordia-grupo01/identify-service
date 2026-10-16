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
// LoginWithGoogle godoc
// @Summary      Log in / sign up with Google
// @Description  CA1: creates a new account if the email is new. CA2: links to an existing account by email. CA3: 503 if Google is unreachable, so the client can offer email/password login instead. If the resolved account has two-factor enabled, the login is accepted (202) but not completed, no cookie is set, and it's finished via POST /v1/login/2fa.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  googleLoginRequest  true  "Google ID token"
// @Success      200  {object}  model.AuthResponse  "no two-factor on this account: session granted"
// @Success      202  {object}  model.TwoFactorChallengeResponse  "two-factor enabled: accepted, continue at POST /v1/login/2fa"
// @Failure      400  {object}  model.ErrorResponse  "missing id_token"
// @Failure      401  {object}  model.ErrorResponse  "token could not be validated"
// @Failure      403  {object}  model.ErrorResponse  "the account is suspended (ACCOUNT_SUSPENDED)"
// @Failure      503  {object}  model.ErrorResponse  "Google unreachable"
// @Router       /v1/oauth/google [post]
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
		case errors.Is(err, service.ErrAccountSuspended):
			respondAccountSuspended(c)

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

	if res.Challenge != nil {
		c.JSON(http.StatusAccepted, res.Challenge)
		return
	}

	setRefreshCookie(c, res.Auth.RefreshToken)
	res.Auth.RefreshToken = ""
	c.JSON(http.StatusOK, res.Auth)
}
