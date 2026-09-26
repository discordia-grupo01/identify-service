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

// @Summary      Get the account's two-factor status
// @Description  Whether 2FA is active, pending confirmation (QR requested but not scanned yet), or off, plus how many recovery codes are left.
// @Tags         two-factor
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  model.TwoFactorStatusResponse
// @Failure      401  {object}  model.ErrorResponse
// @Router       /v1/me/2fa [get]
func (h *TwoFactorHandler) Status(c *gin.Context) {
	status, err := h.service.Status(c.Request.Context(), c.GetString(middleware.UserIDKey))
	if err != nil {
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, status)
}

// @Summary      Start setting up two-factor authentication
// @Description  Generates a TOTP secret and returns the otpauth:// URI for the front to render as a QR. 2FA stays off until a code is confirmed via /v1/me/2fa/activate. Calling this twice while pending reuses the same secret instead of rotating it.
// @Tags         two-factor
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  model.TwoFactorSetupResponse
// @Failure      401  {object}  model.ErrorResponse
// @Failure      409  {object}  model.ErrorResponse  "2FA is already enabled on this account"
// @Router       /v1/me/2fa/setup [post]
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

// @Summary      Confirm the QR and activate two-factor authentication
// @Description  Validates a code from the authenticator app against the pending secret. On success, activates 2FA and returns the one-time recovery codes -- they are shown here only, afterwards only their hash is kept.
// @Tags         two-factor
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  model.TwoFactorActivateRequest  true  "6-digit code from the authenticator app"
// @Success      200  {object}  model.TwoFactorRecoveryCodesResponse
// @Failure      400  {object}  model.ErrorResponse  "missing code, or the code is wrong/expired"
// @Failure      401  {object}  model.ErrorResponse
// @Failure      409  {object}  model.ErrorResponse  "no pending setup, or 2FA is already enabled"
// @Router       /v1/me/2fa/activate [post]
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

// @Summary      Disable two-factor authentication
// @Description  Requires the account password again, so an already-open session (a borrowed laptop, a forgotten tab) can't strip 2FA from the account by itself. Login stops requesting the second factor right away.
// @Tags         two-factor
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  model.TwoFactorPasswordRequest  true  "Account password"
// @Success      204  "2FA disabled"
// @Failure      400  {object}  model.ErrorResponse  "missing password"
// @Failure      401  {object}  model.ErrorResponse
// @Failure      403  {object}  model.ErrorResponse  "wrong password -- not 401, so the front doesn't treat it as an expired session"
// @Failure      409  {object}  model.ErrorResponse  "2FA is not enabled on this account"
// @Router       /v1/me/2fa/disable [post]
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

// @Summary      Regenerate the recovery code list
// @Description  Requires the account password again. Replaces the whole list: every previous recovery code, used or not, stops working. Recommended after using one.
// @Tags         two-factor
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  model.TwoFactorPasswordRequest  true  "Account password"
// @Success      200  {object}  model.TwoFactorRecoveryCodesResponse
// @Failure      400  {object}  model.ErrorResponse  "missing password"
// @Failure      401  {object}  model.ErrorResponse
// @Failure      403  {object}  model.ErrorResponse  "wrong password"
// @Failure      409  {object}  model.ErrorResponse  "2FA is not enabled on this account"
// @Router       /v1/me/2fa/recovery-codes [post]
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

// @Summary      Complete a login that requires two-factor
// @Description  Second step of the login: takes the challenge_token from /v1/login and either a 6-digit code from the authenticator app or a recovery code -- same endpoint for both. On success, sets the refresh token as an httpOnly cookie and grants a session. The challenge allows up to 5 attempts within 5 minutes; either limit exhausted means starting over from /v1/login.
// @Tags         two-factor
// @Accept       json
// @Produce      json
// @Param        body  body  model.TwoFactorVerifyRequest  true  "Challenge token plus a TOTP or recovery code"
// @Success      200  {object}  model.TwoFactorVerifyResponse
// @Failure      400  {object}  model.ErrorResponse  "missing challenge_token or code"
// @Failure      401  {object}  model.ErrorResponse  "wrong/expired code, or the challenge itself expired/was already used"
// @Failure      429  {object}  model.ErrorResponse  "too many failed attempts on this challenge"
// @Router       /v1/login/2fa [post]
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
