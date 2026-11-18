package handler

import (
	"errors"
	"net/http"

	"github.com/discordia-grupo01/identify-service/internal/apierr"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"

	"github.com/gin-gonic/gin"
)

type AccountRecoveryHandler struct {
	service service.AccountRecoveryService
}

func NewAccountRecoveryHandler(service service.AccountRecoveryService) *AccountRecoveryHandler {
	return &AccountRecoveryHandler{service: service}
}

// @Summary      Ask staff to recover an account that lost its second factor
// @Description  For a user with 2FA on who lost both the authenticator app and every recovery code. Sent from the second step of the login with the challenge_token from /v1/login (or /v1/oauth/google), which proves the first factor; the challenge is not consumed, so the user can still go back and verify. The request stays pending until staff approves or rejects it from the back-office, and the user gets the answer by email. Only one pending request per user.
// @Tags         two-factor
// @Accept       json
// @Produce      json
// @Param        body  body  model.AccountRecoveryCreateRequest  true  "Challenge token plus an optional message (up to 500 characters)"
// @Success      201  {object}  model.AccountRecoveryCreateResponse
// @Failure      400  {object}  apierr.ErrorResponse  "missing challenge_token, or the message is longer than 500 characters"
// @Failure      401  {object}  apierr.ErrorResponse  "the challenge expired, was already used or doesn't exist (TWO_FACTOR_CHALLENGE_EXPIRED)"
// @Failure      403  {object}  apierr.ErrorResponse  "the account is suspended (ACCOUNT_SUSPENDED)"
// @Failure      409  {object}  apierr.ErrorResponse  "the user already has a pending request (ACCOUNT_RECOVERY_ALREADY_PENDING)"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/login/2fa/recovery-request [post]
func (h *AccountRecoveryHandler) Create(c *gin.Context) {
	var req model.AccountRecoveryCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.InvalidBodyFromBinding(c, "Falta el token de verificación", err)
		return
	}

	res, err := h.service.Request(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAccountRecoveryMessageTooLong):
			apierr.Abort(c, http.StatusBadRequest, apierr.CodeAccountRecoveryMessageTooLong, "El mensaje no puede superar los 500 caracteres", nil)
		case errors.Is(err, service.ErrTwoFactorChallengeInvalid):
			apierr.Abort(c, http.StatusUnauthorized, apierr.CodeTwoFactorChallengeExpired, "La verificación venció. Volvé a iniciar sesión para enviar la solicitud.", nil)
		case errors.Is(err, service.ErrAccountSuspended):
			apierr.AccountSuspended(c)
		case errors.Is(err, service.ErrAccountRecoveryAlreadyPending):
			apierr.Abort(c, http.StatusConflict, apierr.CodeAccountRecoveryAlreadyPending, "Ya tenés una solicitud de recuperación pendiente. Te vamos a avisar por email cuando la revisemos.", nil)
		default:
			apierr.Internal(c)
		}
		return
	}

	c.JSON(http.StatusCreated, res)
}
