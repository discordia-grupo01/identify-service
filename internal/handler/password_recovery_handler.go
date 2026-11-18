package handler

import (
	"errors"
	"net/http"

	"github.com/discordia-grupo01/identify-service/internal/apierr"
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

// RecoverPassword godoc
// @Summary      Request a password recovery email
// @Description  Always responds 202 regardless of whether the email is registered (doesn't leak which emails exist). Rate-limited per email.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  model.PasswordRecoveryRequest  true  "Email"
// @Success      202  {object}  map[string]string
// @Failure      400  {object}  apierr.ErrorResponse
// @Failure      429  {object}  apierr.ErrorResponse  "too many requests for this email"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/password-recovery [post]
func (h *PasswordRecoveryHandler) RecoverPassword(c *gin.Context) {
	var request model.PasswordRecoveryRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apierr.InvalidBodyFromBinding(c, "Formato de petición o correo electrónico inválido", err)
		return
	}

	if err := h.service.RecoverPassword(c.Request.Context(), request); err != nil {
		if errors.Is(err, service.ErrRecoveryRateLimited) {
			apierr.Abort(c, http.StatusTooManyRequests, apierr.CodeRecoveryRateLimited, "Se alcanzó el límite de solicitudes. Intenta nuevamente más tarde", nil)
			return
		}
		apierr.Internal(c)
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Si el correo está registrado, recibirás un enlace para recuperar tu contraseña"})
}

// ResetPassword godoc
// @Summary      Reset the password using a recovery token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  model.PasswordResetRequest  true  "Token, new password and confirmation"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  apierr.ErrorResponse  "passwords don't match, insecure password, or invalid/expired token"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/password-reset [post]
func (h *PasswordRecoveryHandler) ResetPassword(c *gin.Context) {
	var request model.PasswordResetRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apierr.InvalidBodyFromBinding(c, "Formato de petición inválido", err)
		return
	}

	if err := h.service.ResetPassword(c.Request.Context(), request); err != nil {
		switch {
		case errors.Is(err, service.ErrPasswordsDoNotMatch):
			apierr.Abort(c, http.StatusBadRequest, apierr.CodePasswordsDoNotMatch, "Las contraseñas no coinciden", nil)
		case errors.Is(err, service.ErrPasswordInsecure):
			apierr.Abort(c, http.StatusBadRequest, apierr.CodeInsecurePassword, err.Error(), nil)
		case errors.Is(err, service.ErrInvalidResetToken):
			apierr.Abort(c, http.StatusBadRequest, apierr.CodeInvalidResetToken, "El enlace de recuperación no es válido o expiró. Solicita uno nuevo", nil)
		default:
			apierr.Internal(c)
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "La contraseña fue actualizada correctamente"})
}
