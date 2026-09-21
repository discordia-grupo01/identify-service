package handler

import (
	"errors"
	"net/http"

	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"

	"github.com/gin-gonic/gin"
)

type PinHandler struct {
	service service.PinService
}

func NewPinHandler(service service.PinService) *PinHandler {
	return &PinHandler{service: service}
}

func (h *PinHandler) Configure(c *gin.Context) {
	var req model.ConfigurePinRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.APIError{
				Code:    "INVALID_INPUT",
				Message: "Falta el PIN o el identificador del dispositivo",
				Details: map[string]any{"reason": err.Error()},
			},
		})
		return
	}

	userID := c.GetString(middleware.UserIDKey)

	if err := h.service.ConfigurePin(c.Request.Context(), userID, req.DeviceID, req.Pin); err != nil {
		if errors.Is(err, service.ErrPinInvalidFormat) {
			c.JSON(http.StatusBadRequest, model.ErrorResponse{
				Error: model.APIError{
					Code:    "INVALID_PIN",
					Message: err.Error(),
					Details: map[string]any{"pin_length": 6},
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

	c.Status(http.StatusNoContent)
}

func (h *PinHandler) Login(c *gin.Context) {
	var req model.PinLoginRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.APIError{
				Code:    "INVALID_INPUT",
				Message: "Falta el PIN o el identificador del dispositivo",
				Details: map[string]any{"reason": err.Error()},
			},
		})
		return
	}

	res, err := h.service.LoginWithPin(c.Request.Context(), req.DeviceID, req.Pin)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPinNotConfigured), errors.Is(err, service.ErrPinIncorrect), errors.Is(err, service.ErrPinInvalidFormat):
			c.JSON(http.StatusUnauthorized, model.ErrorResponse{
				Error: model.APIError{
					Code:    "INVALID_PIN",
					Message: "El PIN no es correcto para este dispositivo",
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
