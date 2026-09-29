// Package e2e contiene tests de punta a punta: arman el router real con
// repositorios reales contra un Postgres real (sin stubs), y lo ejercitan vía
// HTTP tal como lo haría un cliente real. Se saltean automáticamente si no
// hay DATABASE_HOST seteado en el entorno.
package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/handler"
	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/service"
	"github.com/discordia-grupo01/identify-service/internal/utils/email"
	"github.com/discordia-grupo01/identify-service/internal/utils/googleoauth"
	"github.com/discordia-grupo01/identify-service/internal/utils/profileimage"
	"github.com/discordia-grupo01/identify-service/internal/utils/twofactor"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

const testJWTSecret = "e2e-test-secret"

type e2eConfirmationMailer struct {
	mu       sync.Mutex
	messages map[string][]email.EmailConfirmationRequest
}

func (m *e2eConfirmationMailer) SendEmailConfirmationEmail(request email.EmailConfirmationRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.messages == nil {
		m.messages = make(map[string][]email.EmailConfirmationRequest)
	}
	m.messages[request.Email] = append(m.messages[request.Email], request)
	return nil
}

func (m *e2eConfirmationMailer) latest(emailAddress string) (email.EmailConfirmationRequest, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	messages := m.messages[emailAddress]
	if len(messages) == 0 {
		return email.EmailConfirmationRequest{}, false
	}
	return messages[len(messages)-1], true
}

type e2ePasswordRecoveryMailer struct {
	mu       sync.Mutex
	messages map[string][]email.PasswordResetRequest
}

func (m *e2ePasswordRecoveryMailer) SendPasswordResetEmail(request email.PasswordResetRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.messages == nil {
		m.messages = make(map[string][]email.PasswordResetRequest)
	}
	m.messages[request.Email] = append(m.messages[request.Email], request)
	return nil
}

func (m *e2ePasswordRecoveryMailer) latest(emailAddress string) (email.PasswordResetRequest, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	messages := m.messages[emailAddress]
	if len(messages) == 0 {
		return email.PasswordResetRequest{}, false
	}
	return messages[len(messages)-1], true
}

// fakeGoogleVerifier stands in for a real call to Google's JWKS endpoint:
// e2e tests can't present a genuine Google-signed ID token, so they hand
// this a claims-by-token map instead. An empty/unknown token is treated as
// invalid, same as the real Verifier would.
type fakeGoogleVerifier struct {
	claimsByToken map[string]*googleoauth.Claims
}

func (v *fakeGoogleVerifier) Verify(_ context.Context, rawIDToken string) (*googleoauth.Claims, error) {
	claims, ok := v.claimsByToken[rawIDToken]
	if !ok {
		return nil, errors.New("invalid id token")
	}
	return claims, nil
}

func resetToken(t *testing.T, request email.PasswordResetRequest) string {
	t.Helper()
	parsed, err := url.Parse(request.ResetURL)
	if err != nil {
		t.Fatalf("parse reset URL: %v", err)
	}
	token := parsed.Query().Get("token")
	if token == "" {
		t.Fatal("password reset email did not contain a token")
	}
	return token
}

func confirmationToken(t *testing.T, request email.EmailConfirmationRequest) string {
	t.Helper()
	parsed, err := url.Parse(request.ConfirmURL)
	if err != nil {
		t.Fatalf("parse confirmation URL: %v", err)
	}
	token := parsed.Query().Get("token")
	if token == "" {
		t.Fatal("confirmation email did not contain a token")
	}
	return token
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	host := os.Getenv("DATABASE_HOST")
	if host == "" {
		t.Skip("DATABASE_HOST no está seteado: este test e2e requiere un Postgres real (docker compose up -d db && make migrate)")
	}

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host,
		getenvDefault("DATABASE_PORT", "5432"),
		getenvDefault("DATABASE_USER", "discordia"),
		getenvDefault("DATABASE_PASSWORD", "discordia"),
		getenvDefault("DATABASE_NAME", "discordia_servicio"),
		getenvDefault("DATABASE_SSLMODE", "disable"),
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("db.Ping() error = %v (¿corriste `make migrate`?)", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`TRUNCATE TABLE refresh_tokens, users, server_memberships, consumed_events, login_attempts, outbox_events RESTART IDENTITY CASCADE`)
		_ = db.Close()
	})

	if _, err := db.Exec(`TRUNCATE TABLE refresh_tokens, users, server_memberships, login_attempts, outbox_events RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	return db
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func newTestRouter(t *testing.T, db *sql.DB) *gin.Engine {
	t.Helper()
	t.Setenv("JWT_SECRET", testJWTSecret)

	gin.SetMode(gin.TestMode)
	router := gin.New()

	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewRefreshTokenRepository(db)
	confirmationRepo := repository.NewPostgresEmailConfirmationRepository(db)
	confirmationMailer := &e2eConfirmationMailer{}
	confirmationService := service.NewEmailConfirmationService(
		confirmationRepo,
		userRepo,
		confirmationMailer,
		"https://e2e.example.com/confirm-email",
	)

	twoFactorCipher, err := twofactor.NewCipher("e2e-two-factor-key")
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	twoFactorService := service.NewTwoFactorService(
		repository.NewTwoFactorRepository(db),
		userRepo,
		sessionRepo,
		twoFactorCipher,
	)
	twoFactorHandler := handler.NewTwoFactorHandler(twoFactorService)

	authService := service.NewAuthService(userRepo, sessionRepo,
		service.WithEmailConfirmation(confirmationService),
		service.WithTwoFactor(twoFactorService),
		service.WithLoginAttempts(repository.NewLoginAttemptRepository(db)),
	)
	authHandler := handler.NewAuthHandler(authService)
	confirmationHandler := handler.NewEmailConfirmationHandler(confirmationService)

	profileStorage := profileimage.NewLocalStorage(t.TempDir(), "/uploads/profile-images")
	membershipRepo := repository.NewServerMembershipRepository(db)
	profileService := service.NewProfileService(userRepo, profileStorage, membershipRepo)
	profileHandler := handler.NewProfileHandler(profileService)

	pinService := service.NewPinService(repository.NewDevicePinRepository(db), userRepo, sessionRepo)
	pinHandler := handler.NewPinHandler(pinService)

	googleVerifier := &fakeGoogleVerifier{claimsByToken: map[string]*googleoauth.Claims{}}
	oauthService := service.NewOAuthService(userRepo, repository.NewOAuthIdentityRepository(db), sessionRepo, googleVerifier)
	oauthHandler := handler.NewOAuthHandler(oauthService)

	passwordRecoveryMailer := &e2ePasswordRecoveryMailer{}
	passwordRecoveryService := service.NewPasswordRecoveryService(
		repository.NewPostgresPasswordRecoveryRepository(db),
		userRepo,
		passwordRecoveryMailer,
	)
	passwordRecoveryHandler := handler.NewPasswordRecoveryHandler(passwordRecoveryService)

	api := router.Group("/v1")
	{
		api.POST("/users", authHandler.Register)
		api.POST("/login", authHandler.Login)
		api.POST("/email-confirmation", confirmationHandler.Request)
		api.POST("/email-confirmation/confirm", confirmationHandler.Confirm)
		api.POST("/refresh", authHandler.Refresh)
		api.POST("/logout", middleware.RequireAuth(), authHandler.Logout)
		api.GET("/me/profile", middleware.RequireAuth(), profileHandler.GetProfile)
		api.PATCH("/me/profile", middleware.RequireAuth(), profileHandler.Update)
		api.GET("/users/:id", middleware.RequireAuth(), profileHandler.GetPublicProfile)
		api.PUT("/me/status", middleware.RequireAuth(), profileHandler.UpdateStatus)
		api.DELETE("/me/status", middleware.RequireAuth(), profileHandler.DeleteStatus)
		api.POST("/login/2fa", twoFactorHandler.VerifyLogin)
		api.GET("/me/2fa", middleware.RequireAuth(), twoFactorHandler.Status)
		api.POST("/me/2fa/setup", middleware.RequireAuth(), twoFactorHandler.Setup)
		api.POST("/me/2fa/activate", middleware.RequireAuth(), twoFactorHandler.Activate)
		api.POST("/me/2fa/disable", middleware.RequireAuth(), twoFactorHandler.Disable)
		api.POST("/me/2fa/recovery-codes", middleware.RequireAuth(), twoFactorHandler.RegenerateRecoveryCodes)
		api.POST("/pin", middleware.RequireAuth(), pinHandler.Configure)
		api.POST("/pin/login", pinHandler.Login)
		api.POST("/oauth/google", oauthHandler.LoginWithGoogle)
		api.POST("/password-recovery", passwordRecoveryHandler.RecoverPassword)
		api.POST("/password-reset", passwordRecoveryHandler.ResetPassword)
	}
	e2eMailers.Store(router, confirmationMailer)
	e2ePasswordMailers.Store(router, passwordRecoveryMailer)
	e2eGoogleVerifiers.Store(router, googleVerifier)

	return router
}

var (
	e2eMailers         sync.Map
	e2ePasswordMailers sync.Map
	e2eGoogleVerifiers sync.Map
)

func confirmationMailerFor(t *testing.T, router *gin.Engine) *e2eConfirmationMailer {
	t.Helper()
	mailer, ok := e2eMailers.Load(router)
	if !ok {
		t.Fatal("confirmation mailer was not registered for test router")
	}
	return mailer.(*e2eConfirmationMailer)
}

func passwordRecoveryMailerFor(t *testing.T, router *gin.Engine) *e2ePasswordRecoveryMailer {
	t.Helper()
	mailer, ok := e2ePasswordMailers.Load(router)
	if !ok {
		t.Fatal("password recovery mailer was not registered for test router")
	}
	return mailer.(*e2ePasswordRecoveryMailer)
}

// googleVerifierFor lets a test register the claims a given ID token should
// resolve to, since there's no real Google to hand the router a genuine one.
func googleVerifierFor(t *testing.T, router *gin.Engine) *fakeGoogleVerifier {
	t.Helper()
	verifier, ok := e2eGoogleVerifiers.Load(router)
	if !ok {
		t.Fatal("google verifier was not registered for test router")
	}
	return verifier.(*fakeGoogleVerifier)
}

// registerAndLogin crea un usuario real (POST /v1/users) y lo loguea
// (POST /v1/login), devolviendo su id y un JWT real y válido.
type testSession struct {
	UserID       string
	AccessToken  string
	RefreshToken string
}

func registerAndLoginSession(t *testing.T, router *gin.Engine, name string, email string, password string) testSession {
	t.Helper()

	registerBody, _ := json.Marshal(model.RegisterRequest{Name: name, Email: email, Password: password})
	registerRecorder := doRequest(router, http.MethodPost, "/v1/users", registerBody, "")
	if registerRecorder.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", registerRecorder.Code, registerRecorder.Body.String())
	}
	var registered model.UserResponse
	decodeJSON(t, registerRecorder, &registered)

	loginBody, _ := json.Marshal(model.LoginRequest{Email: email, Password: password})
	loginRecorder := doRequest(router, http.MethodPost, "/v1/login", loginBody, "")
	if loginRecorder.Code != http.StatusForbidden {
		t.Fatalf("unconfirmed login status = %d, body = %s", loginRecorder.Code, loginRecorder.Body.String())
	}

	confirmationRequest, ok := confirmationMailerFor(t, router).latest(email)
	if !ok {
		t.Fatalf("registration did not send a confirmation email to %s", email)
	}
	confirmBody, _ := json.Marshal(model.EmailConfirmationTokenRequest{Token: confirmationToken(t, confirmationRequest)})
	confirmRecorder := doRequest(router, http.MethodPost, "/v1/email-confirmation/confirm", confirmBody, "")
	if confirmRecorder.Code != http.StatusOK {
		t.Fatalf("confirmation status = %d, body = %s", confirmRecorder.Code, confirmRecorder.Body.String())
	}

	loginRecorder = doRequest(router, http.MethodPost, "/v1/login", loginBody, "")
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRecorder.Code, loginRecorder.Body.String())
	}
	var authResponse model.AuthResponse
	decodeJSON(t, loginRecorder, &authResponse)

	refreshToken := ""
	for _, cookie := range loginRecorder.Result().Cookies() {
		if cookie.Name == "refresh_token" {
			refreshToken = cookie.Value
			break
		}
	}
	if refreshToken == "" {
		t.Fatal("login response did not set a refresh_token cookie")
	}

	return testSession{
		UserID:       registered.ID,
		AccessToken:  authResponse.Token,
		RefreshToken: refreshToken,
	}
}

func registerAndLogin(t *testing.T, router *gin.Engine, name string, email string, password string) (userID string, accessToken string) {
	session := registerAndLoginSession(t, router, name, email, password)
	return session.UserID, session.AccessToken
}

func doRequest(router *gin.Engine, method string, path string, body []byte, bearerToken string) *httptest.ResponseRecorder {
	return doRequestWithRefreshToken(router, method, path, body, bearerToken, "")
}

func doRequestWithRefreshToken(router *gin.Engine, method string, path string, body []byte, bearerToken string, refreshToken string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if refreshToken != "" {
		request.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshToken})
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeJSON(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(recorder.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v (body = %s)", err, recorder.Body.String())
	}
}
