package handler

import (
	"errors"
	"net/http"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"
	"github.com/gin-gonic/gin"
)

type PasswordRecoveryHandler struct {
	service service.PasswordRecoveryService
}

func NewPasswordRecoveryHandler(passwordRecoveryService service.PasswordRecoveryService) *PasswordRecoveryHandler {
	return &PasswordRecoveryHandler{service: passwordRecoveryService}
}

func (h *PasswordRecoveryHandler) RecoverPassword(c *gin.Context) {
	var request model.PasswordRecoveryRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
			Code:    "INVALID_INPUT",
			Message: "Formato de petición o correo electrónico inválido",
		}})
		return
	}

	if err := h.service.RecoverPassword(c.Request.Context(), request); err != nil {
		if errors.Is(err, service.ErrRecoveryRateLimited) {
			c.JSON(http.StatusTooManyRequests, model.ErrorResponse{Error: model.APIError{
				Code:    "RECOVERY_RATE_LIMITED",
				Message: "Se alcanzó el límite de solicitudes. Intenta nuevamente más tarde",
			}})
			return
		}
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Error: model.APIError{
			Code:    "INTERNAL_SERVER_ERROR",
			Message: "Ha ocurrido un error inesperado en el servidor",
		}})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Si el correo está registrado, recibirás un enlace para recuperar tu contraseña"})
}

func (h *PasswordRecoveryHandler) ResetPassword(c *gin.Context) {
	var request model.PasswordResetRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
			Code:    "INVALID_INPUT",
			Message: "Formato de petición inválido",
		}})
		return
	}

	if err := h.service.ResetPassword(c.Request.Context(), request); err != nil {
		switch {
		case errors.Is(err, service.ErrPasswordsDoNotMatch):
			c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
				Code:    "PASSWORDS_DO_NOT_MATCH",
				Message: "Las contraseñas no coinciden",
			}})
		case errors.Is(err, service.ErrPasswordInsecure):
			c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
				Code:    "INSECURE_PASSWORD",
				Message: err.Error(),
			}})
		case errors.Is(err, service.ErrInvalidResetToken):
			c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
				Code:    "INVALID_RESET_TOKEN",
				Message: "El enlace de recuperación no es válido o expiró. Solicita uno nuevo",
			}})
		default:
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Error: model.APIError{
				Code:    "INTERNAL_SERVER_ERROR",
				Message: "Ha ocurrido un error inesperado en el servidor",
			}})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "La contraseña fue actualizada correctamente"})
}
