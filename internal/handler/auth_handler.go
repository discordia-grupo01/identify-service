package handler

import (
	"errors"
	"net/http"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"

	"github.com/gin-gonic/gin"
)

const (
	refreshCookieName = "refresh_token"
	refreshCookiePath = "/v1/auth"
)

type AuthHandler struct {
	service service.AuthService
}

func NewAuthHandler(service service.AuthService) *AuthHandler {
	return &AuthHandler{service: service}
}

// Register godoc
// @Summary      Register a new account
// @Description  Does NOT create a session (no token returned) -- register separately, then log in.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  model.RegisterRequest  true  "Name, email and password"
// @Success      201  {object}  model.UserResponse
// @Failure      400  {object}  model.ErrorResponse  "invalid input, or insecure password"
// @Failure      409  {object}  model.ErrorResponse  "email already in use"
// @Router       /v1/users [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req model.RegisterRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.APIError{
				Code:    "INVALID_INPUT",
				Message: "Formato de petición o correo electrónico inválido",
				Details: map[string]any{"reason": err.Error()},
			},
		})
		return
	}

	res, err := h.service.Register(c.Request.Context(), req)
	if err != nil {
		switch {

		case errors.Is(err, service.ErrPasswordInsecure):
			c.JSON(http.StatusBadRequest, model.ErrorResponse{
				Error: model.APIError{
					Code:    "INSECURE_PASSWORD",
					Message: err.Error(),
					Details: map[string]any{
						"min_length":         8,
						"requires_uppercase": true,
						"requires_lowercase": true,
						"requires_number":    true,
					},
				},
			})

		case errors.Is(err, service.ErrEmailInUse):
			c.JSON(http.StatusConflict, model.ErrorResponse{
				Error: model.APIError{
					Code:    "EMAIL_ALREADY_EXISTS",
					Message: err.Error(),
				},
			})

		case errors.Is(err, service.ErrInvalidProfileName):
			c.JSON(http.StatusBadRequest, model.ErrorResponse{
				Error: model.APIError{
					Code:    "INVALID_PROFILE",
					Message: err.Error(),
					Details: map[string]any{
						"field":      "name",
						"max_length": service.MaxNameRunes,
					},
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

	c.JSON(http.StatusCreated, res)
}

// Login godoc
// @Summary      Log in with email and password
// @Description  Sets the refresh token as an httpOnly cookie; the response body only carries the access token (refresh_token is zeroed out). If the account has two-factor enabled, the credentials alone are not enough: the request is accepted (202) but not completed, no cookie is set, and the login is finished via POST /v1/login/2fa.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  model.LoginRequest  true  "Email and password"
// @Success      200  {object}  model.AuthResponse  "no two-factor on this account: session granted"
// @Success      202  {object}  model.TwoFactorChallengeResponse  "two-factor enabled: accepted, continue at POST /v1/login/2fa"
// @Failure      400  {object}  model.ErrorResponse
// @Failure      401  {object}  model.ErrorResponse  "wrong email/password"
// @Failure      403  {object}  model.ErrorResponse  "email not verified yet"
// @Router       /v1/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req model.LoginRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.APIError{
				Code:    "INVALID_INPUT",
				Message: "Formato de petición o correo electrónico inválido",
				Details: map[string]any{"reason": err.Error()},
			},
		})
		return
	}

	res, err := h.service.Login(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrEmailNotVerified):
			c.JSON(http.StatusForbidden, model.ErrorResponse{Error: model.APIError{
				Code:    "EMAIL_NOT_VERIFIED",
				Message: "Debes confirmar tu correo electrónico antes de iniciar sesión",
			}})
			return
		case errors.Is(err, service.ErrInvalidCredentials):
			c.JSON(http.StatusUnauthorized, model.ErrorResponse{
				Error: model.APIError{
					Code:    "INVALID_CREDENTIALS",
					Message: "El correo electrónico o la contraseña son incorrectos",
				},
			})
			return
		}

		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Error: model.APIError{
				Code:    "INTERNAL_SERVER_ERROR",
				Message: "Ha ocurrido un error inesperado en el servidor",
			},
		})
		return
	}

	if res.Challenge != nil {
		// 202 y no 200: la contrasena fue aceptada pero el login no esta
		// completo, asi que el cuerpo no es una sesion sino un desafio.
		c.JSON(http.StatusAccepted, res.Challenge)
		return
	}

	setRefreshCookie(c, res.Auth.RefreshToken)
	res.Auth.RefreshToken = ""
	c.JSON(http.StatusOK, res.Auth)
}

// Logout godoc
// @Summary      Log out
// @Description  Revokes the refresh token (read from the httpOnly cookie) and clears the cookie.
// @Tags         auth
// @Security     BearerAuth
// @Success      204  "no content"
// @Failure      401  {object}  model.ErrorResponse  "missing/invalid session cookie, or session expired"
// @Router       /v1/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	refreshToken, err := c.Cookie(refreshCookieName)
	if err != nil || refreshToken == "" {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: model.APIError{
			Code:    "UNAUTHORIZED",
			Message: "La sesión no es válida",
		}})
		return
	}

	if err := h.service.Logout(c.Request.Context(), refreshToken); err != nil {
		if err == service.ErrInexistentSession {
			c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: model.APIError{
				Code:    "SESSION_EXPIRED",
				Message: "La sesión expiró o ya no es válida",
			}})
			return
		}
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Error: model.APIError{
			Code:    "INTERNAL_SERVER_ERROR",
			Message: "Ha ocurrido un error inesperado en el servidor",
		}})
		return
	}

	clearRefreshCookie(c)
	c.Status(http.StatusNoContent)
}

// Refresh godoc
// @Summary      Refresh the access token
// @Description  Reads the refresh token from the httpOnly cookie (no bearer token needed -- this is how you get a new access token once the old one expired). Rotates the refresh token and resets the cookie.
// @Tags         auth
// @Produce      json
// @Success      200  {object}  model.AuthResponse
// @Failure      401  {object}  model.ErrorResponse  "missing, invalid, reused, or expired refresh token"
// @Router       /v1/refresh [post]
func (h *AuthHandler) Refresh(c *gin.Context) {
	refreshToken, err := c.Cookie(refreshCookieName)
	if err != nil || refreshToken == "" {
		respondSessionExpired(c)
		return
	}

	res, err := h.service.Refresh(c.Request.Context(), refreshToken)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidRefreshToken), errors.Is(err, service.ErrRefreshReused):
			clearRefreshCookie(c)
			respondSessionExpired(c)
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

func setRefreshCookie(c *gin.Context, refreshToken string) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(refreshCookieName, refreshToken, int(service.RefreshTokenTTL.Seconds()), refreshCookiePath, "", true, true)
}

func clearRefreshCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(refreshCookieName, "", -1, refreshCookiePath, "", true, true)
}

func respondSessionExpired(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, model.ErrorResponse{
		Error: model.APIError{
			Code:    "SESSION_EXPIRED",
			Message: "La sesión expiró o ya no es válida",
			Details: map[string]any{"redirect_to": "/v1/login"},
		},
	})
}
