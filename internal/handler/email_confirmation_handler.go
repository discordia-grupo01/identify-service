package handler

import (
	"errors"
	"net/http"

	"github.com/discordia-grupo01/identify-service/internal/apierr"
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

// Request godoc
// @Summary      Request an email confirmation link
// @Description  Always responds 202 regardless of whether the email is registered or already confirmed. Rate-limited per email.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  model.EmailConfirmationRequest  true  "Email"
// @Success      202  {object}  map[string]string
// @Failure      400  {object}  apierr.ErrorResponse
// @Failure      429  {object}  apierr.ErrorResponse  "too many requests for this email"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/email-confirmation [post]
func (h *EmailConfirmationHandler) Request(c *gin.Context) {
	var request model.EmailConfirmationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apierr.InvalidBodyFromBinding(c, "Formato de petición o correo electrónico inválido", err)
		return
	}

	if err := h.service.RequestEmailConfirmation(c.Request.Context(), request); err != nil {
		if errors.Is(err, service.ErrEmailConfirmationRateLimited) {
			apierr.Abort(c, http.StatusTooManyRequests, apierr.CodeEmailConfirmationRateLimited, "Se alcanzó el límite de solicitudes. Intenta nuevamente más tarde", nil)
			return
		}
		apierr.Internal(c)
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Si el correo está registrado y aún no fue confirmado, recibirás un enlace de confirmación"})
}

// Confirm godoc
// @Summary      Confirm an email address using its token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  model.EmailConfirmationTokenRequest  true  "Confirmation token"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  apierr.ErrorResponse  "invalid, expired, or already used token"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/email-confirmation/confirm [post]
func (h *EmailConfirmationHandler) Confirm(c *gin.Context) {
	var request model.EmailConfirmationTokenRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apierr.InvalidBodyFromBinding(c, "Formato de petición inválido", err)
		return
	}

	if err := h.service.ConfirmEmail(c.Request.Context(), request.Token); err != nil {
		if errors.Is(err, service.ErrEmailConfirmationInvalid) || errors.Is(err, service.ErrEmailConfirmationAlreadyUsed) {
			apierr.Abort(c, http.StatusBadRequest, apierr.CodeInvalidEmailConfirmation, "El enlace de confirmación no es válido, expiró o ya fue utilizado", nil)
			return
		}
		apierr.Internal(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "El correo electrónico fue confirmado correctamente"})
}
