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
		if errors.Is(err, service.ErrInvalidCredentials) {
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

	setRefreshCookie(c, res.RefreshToken)
	res.RefreshToken = ""
	c.JSON(http.StatusOK, res)
}

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
