package handler

import (
	"errors"
	"net/http"

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
// @Failure      400  {object}  model.ErrorResponse  "missing challenge_token, or the message is longer than 500 characters"
// @Failure      401  {object}  model.ErrorResponse  "the challenge expired, was already used or doesn't exist (TWO_FACTOR_CHALLENGE_EXPIRED)"
// @Failure      403  {object}  model.ErrorResponse  "the account is suspended (ACCOUNT_SUSPENDED)"
// @Failure      409  {object}  model.ErrorResponse  "the user already has a pending request (ACCOUNT_RECOVERY_ALREADY_PENDING)"
// @Router       /v1/login/2fa/recovery-request [post]
func (h *AccountRecoveryHandler) Create(c *gin.Context) {
	var req model.AccountRecoveryCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondInvalidInput(c, "Falta el token de verificación", err)
		return
	}

	res, err := h.service.Request(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAccountRecoveryMessageTooLong):
			c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: model.APIError{
				Code:    "ACCOUNT_RECOVERY_MESSAGE_TOO_LONG",
				Message: "El mensaje no puede superar los 500 caracteres",
			}})
		case errors.Is(err, service.ErrTwoFactorChallengeInvalid):
			c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: model.APIError{
				Code:    "TWO_FACTOR_CHALLENGE_EXPIRED",
				Message: "La verificación venció. Volvé a iniciar sesión para enviar la solicitud.",
			}})
		case errors.Is(err, service.ErrAccountSuspended):
			respondAccountSuspended(c)
		case errors.Is(err, service.ErrAccountRecoveryAlreadyPending):
			c.JSON(http.StatusConflict, model.ErrorResponse{Error: model.APIError{
				Code:    "ACCOUNT_RECOVERY_ALREADY_PENDING",
				Message: "Ya tenés una solicitud de recuperación pendiente. Te vamos a avisar por email cuando la revisemos.",
			}})
		default:
			respondInternalError(c)
		}
		return
	}

	c.JSON(http.StatusCreated, res)
}
