package handler

import (
	"errors"
	"net/http"

	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"

	"github.com/gin-gonic/gin"
)

type TwoFactorHandler struct {
	service service.TwoFactorService
}

func NewTwoFactorHandler(service service.TwoFactorService) *TwoFactorHandler {
	return &TwoFactorHandler{service: service}
}

func (h *TwoFactorHandler) Status(c *gin.Context) {
	status, err := h.service.Status(c.Request.Context(), c.GetString(middleware.UserIDKey))
	if err != nil {
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h *TwoFactorHandler) Setup(c *gin.Context) {
	res, err := h.service.StartSetup(c.Request.Context(), c.GetString(middleware.UserIDKey))
	if err != nil {
		if errors.Is(err, service.ErrTwoFactorAlreadyEnabled) {
			c.JSON(http.StatusConflict, model.ErrorResponse{Error: model.APIError{
				Code:    "TWO_FACTOR_ALREADY_ENABLED",
				Message: "El segundo factor ya está activo en esta cuenta",
			}})
			return
		}
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *TwoFactorHandler) Activate(c *gin.Context) {
	var req model.TwoFactorActivateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondInvalidInput(c, "Falta el código de verificación", err)
		return
	}

	codes, err := h.service.ConfirmSetup(c.Request.Context(), c.GetString(middleware.UserIDKey), req.Code)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTwoFactorSetupMissing):
			c.JSON(http.StatusConflict, model.ErrorResponse{Error: model.APIError{
				Code:    "TWO_FACTOR_SETUP_MISSING",
				Message: "Primero hay que escanear el código QR",
			}})
		case errors.Is(err, service.ErrTwoFactorAlreadyEnabled):
			c.JSON(http.StatusConflict, model.ErrorResponse{Error: model.APIError{
				Code:    "TWO_FACTOR_ALREADY_ENABLED",
				Message: "El segundo factor ya está activo en esta cuenta",
			}})
		case errors.Is(err, service.ErrTwoFactorCodeInvalid):
			// 400 y no 401: la sesión es válida, lo que está mal es el código
			// que se tipeó. Un 401 acá haría que el front creyera que la
			// sesión venció y mandaría al login.
			c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
				Code:    "INVALID_TWO_FACTOR_CODE",
				Message: "El código no es correcto. Revisá que sea el actual de tu app.",
			}})
		default:
			respondInternalError(c)
		}
		return
	}

	c.JSON(http.StatusOK, model.TwoFactorRecoveryCodesResponse{RecoveryCodes: codes})
}

func (h *TwoFactorHandler) Disable(c *gin.Context) {
	var req model.TwoFactorPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondInvalidInput(c, "Falta la contraseña", err)
		return
	}

	if err := h.service.DisableWithPassword(c.Request.Context(), c.GetString(middleware.UserIDKey), req.Password); err != nil {
		respondTwoFactorPasswordError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *TwoFactorHandler) RegenerateRecoveryCodes(c *gin.Context) {
	var req model.TwoFactorPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondInvalidInput(c, "Falta la contraseña", err)
		return
	}

	codes, err := h.service.RegenerateRecoveryCodes(c.Request.Context(), c.GetString(middleware.UserIDKey), req.Password)
	if err != nil {
		respondTwoFactorPasswordError(c, err)
		return
	}
	c.JSON(http.StatusOK, model.TwoFactorRecoveryCodesResponse{RecoveryCodes: codes})
}

func (h *TwoFactorHandler) VerifyLogin(c *gin.Context) {
	var req model.TwoFactorVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondInvalidInput(c, "Falta el código de verificación", err)
		return
	}

	res, err := h.service.VerifyLogin(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTwoFactorCodeInvalid):
			c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: model.APIError{
				Code:    "INVALID_TWO_FACTOR_CODE",
				Message: "El código no es correcto o ya expiró",
			}})
		case errors.Is(err, service.ErrTwoFactorTooManyAttempts):
			c.JSON(http.StatusTooManyRequests, model.ErrorResponse{Error: model.APIError{
				Code:    "TWO_FACTOR_TOO_MANY_ATTEMPTS",
				Message: "Demasiados intentos fallidos. Volvé a iniciar sesión.",
			}})
		case errors.Is(err, service.ErrTwoFactorChallengeInvalid):
			c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: model.APIError{
				Code:    "TWO_FACTOR_CHALLENGE_EXPIRED",
				Message: "La verificación expiró. Volvé a iniciar sesión.",
			}})
		default:
			respondInternalError(c)
		}
		return
	}

	setRefreshCookie(c, res.RefreshToken)
	res.RefreshToken = ""
	c.JSON(http.StatusOK, res)
}

func respondTwoFactorPasswordError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPasswordMismatch):
		c.JSON(http.StatusForbidden, model.ErrorResponse{Error: model.APIError{
			Code:    "INVALID_PASSWORD",
			Message: "La contraseña es incorrecta",
		}})
	case errors.Is(err, service.ErrTwoFactorNotEnabled):
		c.JSON(http.StatusConflict, model.ErrorResponse{Error: model.APIError{
			Code:    "TWO_FACTOR_NOT_ENABLED",
			Message: "El segundo factor no está activo en esta cuenta",
		}})
	default:
		respondInternalError(c)
	}
}

func respondInvalidInput(c *gin.Context, message string, err error) {
	c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
		Code:    "INVALID_INPUT",
		Message: message,
		Details: map[string]any{"reason": err.Error()},
	}})
}

func respondInternalError(c *gin.Context) {
	c.JSON(http.StatusInternalServerError, model.ErrorResponse{Error: model.APIError{
		Code:    "INTERNAL_SERVER_ERROR",
		Message: "Ha ocurrido un error inesperado en el servidor",
	}})
}
