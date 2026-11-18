package handler

import (
	"errors"
	"net/http"

	"github.com/discordia-grupo01/identify-service/internal/apierr"
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
// @Failure      400  {object}  apierr.ErrorResponse  "missing fields, or PIN not 6-8 digits"
// @Failure      401  {object}  apierr.ErrorResponse
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/pin [post]
func (h *PinHandler) Configure(c *gin.Context) {
	var req model.ConfigurePinRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.InvalidBodyFromBinding(c, "Falta el PIN o el identificador del dispositivo", err)
		return
	}

	userID := c.GetString(middleware.UserIDKey)

	if err := h.service.ConfigurePin(c.Request.Context(), userID, req.DeviceID, req.Pin); err != nil {
		if errors.Is(err, service.ErrPinInvalidFormat) {
			apierr.Abort(c, http.StatusBadRequest, apierr.CodeInvalidPin, err.Error(), map[string]any{"pin_min_length": 6, "pin_max_length": 8})
			return
		}
		apierr.Internal(c)
		return
	}

	c.Status(http.StatusNoContent)
}

// Login godoc
// @Summary      Log in with a device PIN
// @Description  Alternative to email/password for a device that already configured a PIN. If the account has two-factor enabled, the PIN alone is not enough: the request is accepted (202) but not completed, no cookie is set, and the login is finished via POST /v1/login/2fa. After too many wrong PINs in a row, the device is temporarily locked out of PIN login (423) and must fall back to POST /v1/login with email and password.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  model.PinLoginRequest  true  "Device ID and PIN"
// @Success      200  {object}  model.AuthResponse  "no two-factor on this account: session granted"
// @Success      202  {object}  model.TwoFactorChallengeResponse  "two-factor enabled: accepted, continue at POST /v1/login/2fa"
// @Failure      400  {object}  apierr.ErrorResponse
// @Failure      401  {object}  apierr.ErrorResponse  "PIN not configured, incorrect, or malformed"
// @Failure      403  {object}  apierr.ErrorResponse  "email not verified yet (EMAIL_NOT_VERIFIED), or the account is suspended (ACCOUNT_SUSPENDED)"
// @Failure      423  {object}  apierr.ErrorResponse  "too many failed PIN attempts: temporarily locked, fall back to email+password"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/pin/login [post]
func (h *PinHandler) Login(c *gin.Context) {
	var req model.PinLoginRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.InvalidBodyFromBinding(c, "Falta el PIN o el identificador del dispositivo", err)
		return
	}

	res, err := h.service.LoginWithPin(c.Request.Context(), req.DeviceID, req.Pin)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrEmailNotVerified):
			apierr.Abort(c, http.StatusForbidden, apierr.CodeEmailNotVerified, "Debes confirmar tu correo electrónico antes de iniciar sesión", nil)
		case errors.Is(err, service.ErrAccountSuspended):
			apierr.AccountSuspended(c)
		case errors.Is(err, service.ErrPinLocked):
			details := map[string]any{
				"fallback": "email_password",
			}
			var lockedErr *service.PinLockedError
			if errors.As(err, &lockedErr) {
				details["retry_after_seconds"] = int(lockedErr.RetryAfter.Seconds())
			}
			apierr.Abort(c, http.StatusLocked, apierr.CodePinLocked, "Demasiados intentos fallidos. El acceso por PIN quedó bloqueado temporalmente: iniciá sesión con tu email y contraseña.", details)
		case errors.Is(err, service.ErrPinNotConfigured), errors.Is(err, service.ErrPinIncorrect), errors.Is(err, service.ErrPinInvalidFormat):
			apierr.Abort(c, http.StatusUnauthorized, apierr.CodeInvalidPin, "El PIN no es correcto para este dispositivo", nil)
		default:
			apierr.Internal(c)
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
