package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/apierr"
	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"

	"github.com/gin-gonic/gin"
)

const (
	refreshCookieName = "refresh_token"
	refreshCookiePath = "/v1/"
)

type AuthHandler struct {
	service service.AuthService
}

func NewAuthHandler(service service.AuthService) *AuthHandler {
	return &AuthHandler{service: service}
}

// Register godoc
// @Summary      Register a new account
// @Description  Does NOT create a session (no token returned) -- register separately, then log in.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  model.RegisterRequest  true  "Name, email and password"
// @Success      201  {object}  model.UserResponse
// @Failure      400  {object}  apierr.ErrorResponse  "invalid input, or insecure password"
// @Failure      409  {object}  apierr.ErrorResponse  "email already in use"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/users [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req model.RegisterRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.InvalidBodyFromBinding(c, "Formato de petición o correo electrónico inválido", err)
		return
	}

	res, err := h.service.Register(c.Request.Context(), req)
	if err != nil {
		switch {

		case errors.Is(err, service.ErrPasswordInsecure):
			apierr.Abort(c, http.StatusBadRequest, apierr.CodeInsecurePassword, err.Error(), map[string]any{
				"min_length":         8,
				"requires_uppercase": true,
				"requires_lowercase": true,
				"requires_number":    true,
			})

		case errors.Is(err, service.ErrEmailInUse):
			apierr.Abort(c, http.StatusConflict, apierr.CodeEmailAlreadyExists, err.Error(), nil)

		case errors.Is(err, service.ErrInvalidProfileName):
			apierr.Abort(c, http.StatusBadRequest, apierr.CodeInvalidProfile, err.Error(), map[string]any{
				"field":      "name",
				"max_length": service.MaxNameRunes,
			})

		default:
			apierr.Internal(c)
		}
		return
	}

	c.JSON(http.StatusCreated, res)
}

// SetPassword godoc
// @Summary      Set the account's initial password
// @Description  For an account that never had one (a purely federated Google sign-up): lets it configure a password so it can later re-enter it to disable two-factor or regenerate recovery codes. Rejected if the account already has a password -- this is not a change-password endpoint.
// @Tags         auth
// @Accept       json
// @Security     BearerAuth
// @Param        body  body  model.SetPasswordRequest  true  "New password"
// @Success      204  "no content"
// @Failure      400  {object}  apierr.ErrorResponse  "missing password, or insecure password"
// @Failure      401  {object}  apierr.ErrorResponse
// @Failure      409  {object}  apierr.ErrorResponse  "the account already has a password"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/me/password [post]
func (h *AuthHandler) SetPassword(c *gin.Context) {
	var req model.SetPasswordRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.InvalidBodyFromBinding(c, "Falta la contraseña", err)
		return
	}

	userID := c.GetString(middleware.UserIDKey)

	if err := h.service.SetPassword(c.Request.Context(), userID, req.Password); err != nil {
		switch {
		case errors.Is(err, service.ErrPasswordInsecure):
			apierr.Abort(c, http.StatusBadRequest, apierr.CodeInsecurePassword, err.Error(), map[string]any{
				"min_length":         8,
				"requires_uppercase": true,
				"requires_lowercase": true,
				"requires_number":    true,
			})
		case errors.Is(err, service.ErrPasswordAlreadySet):
			apierr.Abort(c, http.StatusConflict, apierr.CodePasswordAlreadySet, err.Error(), nil)
		default:
			apierr.Internal(c)
		}
		return
	}

	c.Status(http.StatusNoContent)
}

// Login godoc
// @Summary      Log in with email and password
// @Description  Sets the refresh token as an httpOnly cookie; the response body only carries the access token (refresh_token is zeroed out). If the account has two-factor enabled, the credentials alone are not enough: the request is accepted (202) but not completed, no cookie is set, and the login is finished via POST /v1/login/2fa.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  model.LoginRequest  true  "Email and password"
// @Success      200  {object}  model.AuthResponse  "no two-factor on this account: session granted"
// @Success      202  {object}  model.TwoFactorChallengeResponse  "two-factor enabled: accepted, continue at POST /v1/login/2fa"
// @Failure      400  {object}  apierr.ErrorResponse
// @Failure      401  {object}  apierr.ErrorResponse  "wrong email/password"
// @Failure      429  {object}  apierr.ErrorResponse  "too many failed login attempts; includes Retry-After"
// @Failure      403  {object}  apierr.ErrorResponse  "email not verified yet (EMAIL_NOT_VERIFIED), or the account is suspended (ACCOUNT_SUSPENDED)"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req model.LoginRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.InvalidBodyFromBinding(c, "Formato de petición o correo electrónico inválido", err)
		return
	}
	req.IP = c.ClientIP()

	res, err := h.service.Login(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrEmailNotVerified):
			apierr.Abort(c, http.StatusForbidden, apierr.CodeEmailNotVerified, "Debes confirmar tu correo electrónico antes de iniciar sesión", nil)
			return
		case errors.Is(err, service.ErrAccountSuspended):
			apierr.AccountSuspended(c)
			return
		case errors.Is(err, service.ErrInvalidCredentials):
			apierr.Abort(c, http.StatusUnauthorized, apierr.CodeInvalidCredentials, "El correo electrónico o la contraseña son incorrectos", nil)
			return
		case errors.Is(err, service.ErrLoginAttemptsExceeded):
			var rateLimitErr *service.LoginRateLimitError
			if errors.As(err, &rateLimitErr) {
				c.Header("Retry-After", strconv.FormatInt(int64(rateLimitErr.RetryAfter/time.Second), 10))
			}
			apierr.Abort(c, http.StatusTooManyRequests, apierr.CodeLoginAttemptsExceeded, "Demasiados intentos fallidos. Esperá y probá de nuevo", nil)
			return
		}

		apierr.Internal(c)
		return
	}

	if res.Challenge != nil {
		// 202 y no 200: la contrasena fue aceptada pero el login no esta
		// completo, asi que el cuerpo no es una sesion sino un desafio.
		c.JSON(http.StatusAccepted, res.Challenge)
		return
	}

	setRefreshCookie(c, res.Auth.RefreshToken)
	res.Auth.RefreshToken = ""
	c.JSON(http.StatusOK, res.Auth)
}

// Logout godoc
// @Summary      Log out
// @Description  Revokes the refresh token (read from the httpOnly cookie) and clears the cookie.
// @Tags         auth
// @Security     BearerAuth
// @Success      204  "no content"
// @Failure      401  {object}  apierr.ErrorResponse  "missing/invalid session cookie, or session expired"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	refreshToken, err := c.Cookie(refreshCookieName)
	if err != nil || refreshToken == "" {
		apierr.Unauthorized(c)
		return
	}

	if err := h.service.Logout(c.Request.Context(), refreshToken); err != nil {
		if err == service.ErrInexistentSession {
			apierr.Abort(c, http.StatusUnauthorized, apierr.CodeSessionExpired, "La sesión expiró o ya no es válida", nil)
			return
		}
		apierr.Internal(c)
		return
	}

	clearRefreshCookie(c)
	c.Status(http.StatusNoContent)
}

// Refresh godoc
// @Summary      Refresh the access token
// @Description  Reads the refresh token from the httpOnly cookie (no bearer token needed -- this is how you get a new access token once the old one expired). Rotates the refresh token and resets the cookie.
// @Tags         auth
// @Produce      json
// @Success      200  {object}  model.AuthResponse
// @Failure      401  {object}  apierr.ErrorResponse  "missing, invalid, reused, or expired refresh token"
// @Failure      403  {object}  apierr.ErrorResponse  "the account is suspended (ACCOUNT_SUSPENDED)"
// @Failure      500  {object}  apierr.ErrorResponse  "unexpected error (INTERNAL_ERROR)"
// @Router       /v1/refresh [post]
func (h *AuthHandler) Refresh(c *gin.Context) {
	refreshToken, err := c.Cookie(refreshCookieName)
	if err != nil || refreshToken == "" {
		apierr.Unauthorized(c)
		return
	}

	res, err := h.service.Refresh(c.Request.Context(), refreshToken)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAccountSuspended):
			clearRefreshCookie(c)
			apierr.AccountSuspended(c)
		case errors.Is(err, service.ErrInvalidRefreshToken), errors.Is(err, service.ErrRefreshReused):
			clearRefreshCookie(c)
			apierr.SessionExpired(c, map[string]any{"redirect_to": "/v1/login"})
		default:
			apierr.Internal(c)
		}
		return
	}

	setRefreshCookie(c, res.RefreshToken)
	res.RefreshToken = ""
	c.JSON(http.StatusOK, res)
}

func setRefreshCookie(c *gin.Context, refreshToken string) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(refreshCookieName, refreshToken, int(service.RefreshTokenTTL.Seconds()), refreshCookiePath, "", true, true)
}

func clearRefreshCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(refreshCookieName, "", -1, refreshCookiePath, "", true, true)
}
