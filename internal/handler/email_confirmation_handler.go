package handler

import (
	"errors"
	"net/http"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"
	"github.com/gin-gonic/gin"
)

type EmailConfirmationHandler struct {
	service service.EmailConfirmationService
}

func NewEmailConfirmationHandler(emailConfirmationService service.EmailConfirmationService) *EmailConfirmationHandler {
	return &EmailConfirmationHandler{service: emailConfirmationService}
}

func (h *EmailConfirmationHandler) Request(c *gin.Context) {
	var request model.EmailConfirmationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
			Code:    "INVALID_INPUT",
			Message: "Formato de petición o correo electrónico inválido",
		}})
		return
	}

	if err := h.service.RequestEmailConfirmation(c.Request.Context(), request); err != nil {
		if errors.Is(err, service.ErrEmailConfirmationRateLimited) {
			c.JSON(http.StatusTooManyRequests, model.ErrorResponse{Error: model.APIError{
				Code:    "EMAIL_CONFIRMATION_RATE_LIMITED",
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

	c.JSON(http.StatusAccepted, gin.H{"message": "Si el correo está registrado y aún no fue confirmado, recibirás un enlace de confirmación"})
}

func (h *EmailConfirmationHandler) Confirm(c *gin.Context) {
	var request model.EmailConfirmationTokenRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
			Code:    "INVALID_INPUT",
			Message: "Formato de petición inválido",
		}})
		return
	}

	if err := h.service.ConfirmEmail(c.Request.Context(), request.Token); err != nil {
		if errors.Is(err, service.ErrEmailConfirmationInvalid) || errors.Is(err, service.ErrEmailConfirmationAlreadyUsed) {
			c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
				Code:    "INVALID_EMAIL_CONFIRMATION",
				Message: "El enlace de confirmación no es válido, expiró o ya fue utilizado",
			}})
			return
		}
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Error: model.APIError{
			Code:    "INTERNAL_SERVER_ERROR",
			Message: "Ha ocurrido un error inesperado en el servidor",
		}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "El correo electrónico fue confirmado correctamente"})
}
