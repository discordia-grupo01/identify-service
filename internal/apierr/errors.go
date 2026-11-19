// Package apierr define el formato de error común de la API
// ({"error": {"code", "message", "details"}}) y es el único lugar que arma
// ese cuerpo: ningún handler ni middleware serializa un error a mano.
package apierr

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// Códigos genéricos del catálogo común de la API.
const (
	CodeInvalidBody      = "INVALID_BODY"
	CodeInvalidParameter = "INVALID_PARAMETER"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeSessionExpired   = "SESSION_EXPIRED"
	CodeForbidden        = "FORBIDDEN"
	CodeNotFound         = "NOT_FOUND"
	CodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	CodeInternal         = "INTERNAL_ERROR"
	CodeUnavailable      = "SERVICE_UNAVAILABLE"
)

// Códigos de dominio de identify.
const (
	CodeAccountSuspended              = "ACCOUNT_SUSPENDED"
	CodeEmailNotVerified              = "EMAIL_NOT_VERIFIED"
	CodeInvalidCredentials            = "INVALID_CREDENTIALS"
	CodeLoginAttemptsExceeded         = "LOGIN_ATTEMPTS_EXCEEDED"
	CodeInsecurePassword              = "INSECURE_PASSWORD"
	CodeEmailAlreadyExists            = "EMAIL_ALREADY_EXISTS"
	CodePasswordAlreadySet            = "PASSWORD_ALREADY_SET"
	CodePasswordNotSet                = "PASSWORD_NOT_SET"
	CodeSamePassword                  = "SAME_PASSWORD"
	CodeInvalidProfile                = "INVALID_PROFILE"
	CodeInvalidStatus                 = "INVALID_STATUS"
	CodeUserNotFound                  = "USER_NOT_FOUND"
	CodeInvalidPin                    = "INVALID_PIN"
	CodePinLocked                     = "PIN_LOCKED"
	CodeOAuthProviderUnavailable      = "OAUTH_PROVIDER_UNAVAILABLE"
	CodeOAuthTokenInvalid             = "OAUTH_TOKEN_INVALID"
	CodeRecoveryRateLimited           = "RECOVERY_RATE_LIMITED"
	CodeEmailConfirmationRateLimited  = "EMAIL_CONFIRMATION_RATE_LIMITED"
	CodeInvalidEmailConfirmation      = "INVALID_EMAIL_CONFIRMATION"
	CodePasswordsDoNotMatch           = "PASSWORDS_DO_NOT_MATCH"
	CodeInvalidResetToken             = "INVALID_RESET_TOKEN"
	CodeTwoFactorAlreadyEnabled       = "TWO_FACTOR_ALREADY_ENABLED"
	CodeTwoFactorNotEnabled           = "TWO_FACTOR_NOT_ENABLED"
	CodeTwoFactorSetupMissing         = "TWO_FACTOR_SETUP_MISSING"
	CodePasswordRequiredForTwoFactor  = "PASSWORD_REQUIRED_FOR_TWO_FACTOR"
	CodeInvalidTwoFactorCode          = "INVALID_TWO_FACTOR_CODE"
	CodeTwoFactorTooManyAttempts      = "TWO_FACTOR_TOO_MANY_ATTEMPTS"
	CodeTwoFactorChallengeExpired     = "TWO_FACTOR_CHALLENGE_EXPIRED"
	CodeInvalidPassword               = "INVALID_PASSWORD"
	CodeAccountRecoveryMessageTooLong = "ACCOUNT_RECOVERY_MESSAGE_TOO_LONG"
	CodeAccountRecoveryAlreadyPending = "ACCOUNT_RECOVERY_ALREADY_PENDING"
)

// Body es el contenido de "error". details nunca es null: Abort lo normaliza a {}.
type Body struct {
	Code    string         `json:"code" binding:"required" example:"FORBIDDEN"`
	Message string         `json:"message" binding:"required"`
	Details map[string]any `json:"details" binding:"required"`
}

// ErrorResponse es el sobre de toda respuesta con status >= 400.
type ErrorResponse struct {
	Error Body `json:"error" binding:"required"`
}

// Abort corta la request con el error indicado. details nil se serializa como {}.
func Abort(c *gin.Context, status int, code, message string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	c.AbortWithStatusJSON(status, ErrorResponse{Error: Body{Code: code, Message: message, Details: details}})
}

// InvalidBody responde 400 INVALID_BODY sin información sobre el campo.
func InvalidBody(c *gin.Context, message string) {
	Abort(c, http.StatusBadRequest, CodeInvalidBody, message, nil)
}

// InvalidBodyFromBinding responde 400 INVALID_BODY a partir del error de
// ShouldBindJSON. Nunca expone err.Error(): details lleva sólo field y reason
// fijos cuando se sabe qué campo falló, y {} si el JSON está mal formado.
func InvalidBodyFromBinding(c *gin.Context, message string, err error) {
	Abort(c, http.StatusBadRequest, CodeInvalidBody, message, bindingDetails(err))
}

// InvalidParameter responde 400 INVALID_PARAMETER para un parámetro de path inválido.
func InvalidParameter(c *gin.Context, param, message string) {
	Abort(c, http.StatusBadRequest, CodeInvalidParameter, message, map[string]any{"param": param})
}

// Unauthorized responde 401 UNAUTHORIZED: falta el token o no es válido.
func Unauthorized(c *gin.Context) {
	Abort(c, http.StatusUnauthorized, CodeUnauthorized, "La sesión no es válida", nil)
}

// SessionExpired responde 401 SESSION_EXPIRED: se sabe que la sesión o el token venció.
func SessionExpired(c *gin.Context, details map[string]any) {
	Abort(c, http.StatusUnauthorized, CodeSessionExpired, "La sesión expiró o ya no es válida", details)
}

// AccountSuspended responde 403 ACCOUNT_SUSPENDED.
func AccountSuspended(c *gin.Context) {
	Abort(c, http.StatusForbidden, CodeAccountSuspended, "Tu cuenta está suspendida", nil)
}

func Forbidden(c *gin.Context) {
	Abort(c, http.StatusForbidden, CodeForbidden, "No tenés permisos para realizar esta acción", nil)
}

func NotFound(c *gin.Context) {
	Abort(c, http.StatusNotFound, CodeNotFound, "El recurso solicitado no existe", nil)
}

func MethodNotAllowed(c *gin.Context) {
	Abort(c, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "El método no está permitido para este recurso", nil)
}

// Internal responde 500 INTERNAL_ERROR sin ningún detalle del error original.
func Internal(c *gin.Context) {
	Abort(c, http.StatusInternalServerError, CodeInternal, "Ha ocurrido un error inesperado en el servidor", nil)
}

// ServiceUnavailable responde 503 SERVICE_UNAVAILABLE.
func ServiceUnavailable(c *gin.Context, message string) {
	Abort(c, http.StatusServiceUnavailable, CodeUnavailable, message, nil)
}

// RegisterFallbacks hace que la ruta inexistente y el método no permitido
// respondan con el sobre en vez del cuerpo vacío o de texto plano de Gin.
func RegisterFallbacks(router *gin.Engine) {
	router.HandleMethodNotAllowed = true
	router.NoRoute(NotFound)
	router.NoMethod(MethodNotAllowed)
}

func bindingDetails(err error) map[string]any {
	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) && len(validationErrors) > 0 {
		first := validationErrors[0]
		return map[string]any{"field": snakeCase(first.Field()), "reason": validationReason(first.Tag())}
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return map[string]any{"field": typeErr.Field, "reason": "invalid_type"}
	}
	return nil
}

func validationReason(tag string) string {
	switch tag {
	case "required":
		return "required"
	case "email":
		return "invalid_email"
	default:
		return "invalid"
	}
}

// snakeCase pasa el nombre Go del campo (IDToken, DeviceID) al de su tag json.
func snakeCase(name string) string {
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prevLowerOrDigit := unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])
				acronymEnd := unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if prevLowerOrDigit || acronymEnd {
					b.WriteRune('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
