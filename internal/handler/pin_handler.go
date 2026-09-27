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

// Configure godoc
// @Summary      Set up a device PIN
// @Description  Lets the caller log back in on this device with a short PIN instead of the full password.
// @Tags         auth
// @Accept       json
// @Security     BearerAuth
// @Param        body  body  model.ConfigurePinRequest  true  "Device ID and PIN (6-8 digits)"
// @Success      204  "no content"
// @Failure      400  {object}  model.ErrorResponse  "missing fields, or PIN not 6-8 digits"
// @Failure      401  {object}  model.ErrorResponse
// @Router       /v1/pin [post]
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
					Details: map[string]any{"pin_min_length": 6, "pin_max_length": 8},
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

// Login godoc
// @Summary      Log in with a device PIN
// @Description  Alternative to email/password for a device that already configured a PIN. After too many wrong PINs in a row, the device is temporarily locked out of PIN login (423) and must fall back to POST /v1/login with email and password.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  model.PinLoginRequest  true  "Device ID and PIN"
// @Success      200  {object}  model.AuthResponse
// @Failure      400  {object}  model.ErrorResponse
// @Failure      401  {object}  model.ErrorResponse  "PIN not configured, incorrect, or malformed"
// @Failure      403  {object}  model.ErrorResponse  "email not verified yet"
// @Failure      423  {object}  model.ErrorResponse  "too many failed PIN attempts: temporarily locked, fall back to email+password"
// @Router       /v1/pin/login [post]
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
		case errors.Is(err, service.ErrEmailNotVerified):
			c.JSON(http.StatusForbidden, model.ErrorResponse{Error: model.APIError{
				Code:    "EMAIL_NOT_VERIFIED",
				Message: "Debes confirmar tu correo electrónico antes de iniciar sesión",
			}})
		case errors.Is(err, service.ErrPinLocked):
			details := map[string]any{
				"fallback": "email_password",
			}
			var lockedErr *service.PinLockedError
			if errors.As(err, &lockedErr) {
				details["retry_after_seconds"] = int(lockedErr.RetryAfter.Seconds())
			}
			c.JSON(http.StatusLocked, model.ErrorResponse{
				Error: model.APIError{
					Code:    "PIN_LOCKED",
					Message: "Demasiados intentos fallidos. El acceso por PIN quedó bloqueado temporalmente: iniciá sesión con tu email y contraseña.",
					Details: details,
				},
			})
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
