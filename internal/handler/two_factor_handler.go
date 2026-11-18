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
// @Failure      401  {object}  apierr.ErrorResponse
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/me/2fa [get]
func (h *TwoFactorHandler) Status(c *gin.Context) {
	status, err := h.service.Status(c.Request.Context(), c.GetString(middleware.UserIDKey))
	if err != nil {
		apierr.Internal(c)
		return
	}
	c.JSON(http.StatusOK, status)
}

// @Summary      Start setting up two-factor authentication
// @Description  Generates a TOTP secret and returns the otpauth:// URI for the front to render as a QR. 2FA stays off until a code is confirmed via /v1/me/2fa/activate. Calling this twice while pending reuses the same secret instead of rotating it. Requires the account to already have a password (POST /v1/me/password first) -- otherwise there'd be no way to re-enter it later to disable 2FA or regenerate recovery codes.
// @Tags         two-factor
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  model.TwoFactorSetupResponse
// @Failure      401  {object}  apierr.ErrorResponse
// @Failure      409  {object}  apierr.ErrorResponse  "2FA is already enabled on this account, or the account has no password yet"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/me/2fa/setup [post]
func (h *TwoFactorHandler) Setup(c *gin.Context) {
	res, err := h.service.StartSetup(c.Request.Context(), c.GetString(middleware.UserIDKey))
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTwoFactorAlreadyEnabled):
			apierr.Abort(c, http.StatusConflict, apierr.CodeTwoFactorAlreadyEnabled, "El segundo factor ya está activo en esta cuenta", nil)
		case errors.Is(err, service.ErrPasswordRequiredForTwoFactor):
			apierr.Abort(c, http.StatusConflict, apierr.CodePasswordRequiredForTwoFactor, err.Error(), nil)
		default:
			apierr.Internal(c)
		}
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
// @Failure      400  {object}  apierr.ErrorResponse  "missing code, or the code is wrong/expired"
// @Failure      401  {object}  apierr.ErrorResponse
// @Failure      409  {object}  apierr.ErrorResponse  "no pending setup, or 2FA is already enabled"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/me/2fa/activate [post]
func (h *TwoFactorHandler) Activate(c *gin.Context) {
	var req model.TwoFactorActivateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.InvalidBodyFromBinding(c, "Falta el código de verificación", err)
		return
	}

	codes, err := h.service.ConfirmSetup(c.Request.Context(), c.GetString(middleware.UserIDKey), req.Code)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTwoFactorSetupMissing):
			apierr.Abort(c, http.StatusConflict, apierr.CodeTwoFactorSetupMissing, "Primero hay que escanear el código QR", nil)
		case errors.Is(err, service.ErrTwoFactorAlreadyEnabled):
			apierr.Abort(c, http.StatusConflict, apierr.CodeTwoFactorAlreadyEnabled, "El segundo factor ya está activo en esta cuenta", nil)
		case errors.Is(err, service.ErrTwoFactorCodeInvalid):
			// 400 y no 401: la sesión es válida, lo que está mal es el código
			// que se tipeó. Un 401 acá haría que el front creyera que la
			// sesión venció y mandaría al login.
			apierr.Abort(c, http.StatusBadRequest, apierr.CodeInvalidTwoFactorCode, "El código no es correcto. Revisá que sea el actual de tu app.", nil)
		default:
			apierr.Internal(c)
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
// @Failure      400  {object}  apierr.ErrorResponse  "missing password"
// @Failure      401  {object}  apierr.ErrorResponse
// @Failure      403  {object}  apierr.ErrorResponse  "wrong password -- not 401, so the front doesn't treat it as an expired session"
// @Failure      409  {object}  apierr.ErrorResponse  "2FA is not enabled on this account"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/me/2fa/disable [post]
func (h *TwoFactorHandler) Disable(c *gin.Context) {
	var req model.TwoFactorPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.InvalidBodyFromBinding(c, "Falta la contraseña", err)
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
// @Failure      400  {object}  apierr.ErrorResponse  "missing password"
// @Failure      401  {object}  apierr.ErrorResponse
// @Failure      403  {object}  apierr.ErrorResponse  "wrong password"
// @Failure      409  {object}  apierr.ErrorResponse  "2FA is not enabled on this account"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/me/2fa/recovery-codes [post]
func (h *TwoFactorHandler) RegenerateRecoveryCodes(c *gin.Context) {
	var req model.TwoFactorPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.InvalidBodyFromBinding(c, "Falta la contraseña", err)
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
// @Failure      400  {object}  apierr.ErrorResponse  "missing challenge_token or code"
// @Failure      401  {object}  apierr.ErrorResponse  "wrong/expired code, or the challenge itself expired/was already used"
// @Failure      403  {object}  apierr.ErrorResponse  "the account was suspended after the challenge was issued (ACCOUNT_SUSPENDED)"
// @Failure      429  {object}  apierr.ErrorResponse  "too many failed attempts on this challenge"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/login/2fa [post]
func (h *TwoFactorHandler) VerifyLogin(c *gin.Context) {
	var req model.TwoFactorVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.InvalidBodyFromBinding(c, "Falta el código de verificación", err)
		return
	}

	res, err := h.service.VerifyLogin(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAccountSuspended):
			apierr.AccountSuspended(c)
		case errors.Is(err, service.ErrTwoFactorCodeInvalid):
			apierr.Abort(c, http.StatusUnauthorized, apierr.CodeInvalidTwoFactorCode, "El código no es correcto o ya expiró", nil)
		case errors.Is(err, service.ErrTwoFactorTooManyAttempts):
			apierr.Abort(c, http.StatusTooManyRequests, apierr.CodeTwoFactorTooManyAttempts, "Demasiados intentos fallidos. Volvé a iniciar sesión.", nil)
		case errors.Is(err, service.ErrTwoFactorChallengeInvalid):
			apierr.Abort(c, http.StatusUnauthorized, apierr.CodeTwoFactorChallengeExpired, "La verificación expiró. Volvé a iniciar sesión.", nil)
		default:
			apierr.Internal(c)
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
		apierr.Abort(c, http.StatusForbidden, apierr.CodeInvalidPassword, "La contraseña es incorrecta", nil)
	case errors.Is(err, service.ErrTwoFactorNotEnabled):
		apierr.Abort(c, http.StatusConflict, apierr.CodeTwoFactorNotEnabled, "El segundo factor no está activo en esta cuenta", nil)
	default:
		apierr.Internal(c)
	}
}
