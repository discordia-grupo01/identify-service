package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/googleoauth"
)

const suspendedPassword = "Passw0rd!"

// applySuspension simula el efecto de un admin.user_suspended /
// admin.user_reactivated ya verificado.
func applySuspension(t *testing.T, repo repository.UserRepository, userID string, suspended bool, version int64) {
	t.Helper()
	result, err := repo.ApplySuspension(context.Background(), userID, suspended, time.Now().UTC(), version)
	if err != nil || result != repository.SuspensionApplied {
		t.Fatalf("ApplySuspension(%s, %v, v%d) = %v, %v; want SuspensionApplied", userID, suspended, version, result, err)
	}
}

func requireAccountSuspended(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body = %s)", recorder.Code, recorder.Body.String())
	}
	var body model.ErrorResponse
	decodeJSON(t, recorder, &body)
	if body.Error.Code != "ACCOUNT_SUSPENDED" {
		t.Fatalf("error code = %q, want ACCOUNT_SUSPENDED", body.Error.Code)
	}
}

func passwordLogin(router *gin.Engine, email string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(model.LoginRequest{Email: email, Password: suspendedPassword})
	return doRequest(router, http.MethodPost, "/v1/login", body, "")
}

// Login con contraseña: 403 ACCOUNT_SUSPENDED sin sesión; con la contraseña
// equivocada sigue siendo 401 (no se revela el estado a quien no se autentica).
// Reactivado, vuelve a entrar sin ninguna otra acción.
func TestSuspendedUser_PasswordLogin(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	session := registerAndLoginSession(t, router, "Ana", "ana-susp@example.com", suspendedPassword)

	applySuspension(t, repo, session.UserID, true, 1)

	recorder := passwordLogin(router, "ana-susp@example.com")
	requireAccountSuspended(t, recorder)
	if refreshCookie(recorder) != "" {
		t.Error("a suspended login must not set the refresh_token cookie")
	}
	wrong, _ := json.Marshal(model.LoginRequest{Email: "ana-susp@example.com", Password: "Wrong1234"})
	if rec := doRequest(router, http.MethodPost, "/v1/login", wrong, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password status = %d, want 401", rec.Code)
	}

	applySuspension(t, repo, session.UserID, false, 2)
	if rec := passwordLogin(router, "ana-susp@example.com"); rec.Code != http.StatusOK {
		t.Fatalf("login after reactivation status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// Login con Google, tanto de una cuenta creada por Google como de una
// vinculada por email.
func TestSuspendedUser_GoogleLogin(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	googleVerifierFor(t, router).claimsByToken["bruno-token"] = &googleoauth.Claims{
		Subject: "google-subject-bruno", Email: "bruno-susp@example.com", EmailVerified: true, Name: "Bruno",
	}

	first := doRequest(router, http.MethodPost, "/v1/oauth/google", googleLoginRequestBody("bruno-token"), "")
	if first.Code != http.StatusOK {
		t.Fatalf("first google login status = %d, body = %s", first.Code, first.Body.String())
	}
	var auth model.AuthResponse
	decodeJSON(t, first, &auth)

	applySuspension(t, repo, auth.User.ID, true, 1)
	recorder := doRequest(router, http.MethodPost, "/v1/oauth/google", googleLoginRequestBody("bruno-token"), "")
	requireAccountSuspended(t, recorder)
	if refreshCookie(recorder) != "" {
		t.Error("a suspended google login must not set the refresh_token cookie")
	}

	applySuspension(t, repo, auth.User.ID, false, 2)
	if rec := doRequest(router, http.MethodPost, "/v1/oauth/google", googleLoginRequestBody("bruno-token"), ""); rec.Code != http.StatusOK {
		t.Fatalf("google login after reactivation status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// Login con PIN de dispositivo.
func TestSuspendedUser_PinLogin(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	session := registerAndLoginSession(t, router, "Carla", "carla-susp@example.com", suspendedPassword)
	configure, _ := json.Marshal(model.ConfigurePinRequest{Pin: "123456", DeviceID: "device-susp"})
	if rec := doRequest(router, http.MethodPost, "/v1/pin", configure, session.AccessToken); rec.Code != http.StatusNoContent {
		t.Fatalf("configure pin status = %d, body = %s", rec.Code, rec.Body.String())
	}
	pinLogin, _ := json.Marshal(model.PinLoginRequest{Pin: "123456", DeviceID: "device-susp"})

	applySuspension(t, repo, session.UserID, true, 1)
	requireAccountSuspended(t, doRequest(router, http.MethodPost, "/v1/pin/login", pinLogin, ""))

	applySuspension(t, repo, session.UserID, false, 2)
	if rec := doRequest(router, http.MethodPost, "/v1/pin/login", pinLogin, ""); rec.Code != http.StatusOK {
		t.Fatalf("pin login after reactivation status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// 2FA: una cuenta suspendida no recibe desafío; y quien recibió el desafío
// antes de ser suspendido y lo completa después, es frenado igual.
func TestSuspendedUser_TwoFactor(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	const email = "diego-susp@example.com"
	session := registerAndLoginSession(t, router, "Diego", email, suspendedPassword)

	setupRecorder := doRequest(router, http.MethodPost, "/v1/me/2fa/setup", nil, session.AccessToken)
	var setup model.TwoFactorSetupResponse
	decodeJSON(t, setupRecorder, &setup)
	activate, _ := json.Marshal(model.TwoFactorActivateRequest{Code: totpCode(t, setup.Secret, time.Now())})
	if rec := doRequest(router, http.MethodPost, "/v1/me/2fa/activate", activate, session.AccessToken); rec.Code != http.StatusOK {
		t.Fatalf("activate status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Desafío emitido antes de la suspensión...
	challengeToken := loginChallenge(t, router, email, suspendedPassword)
	if challengeToken == "" {
		t.Fatal("login with 2FA active must return a challenge")
	}
	applySuspension(t, repo, session.UserID, true, 1)

	// ...completado después: issueTokenPair lo frena.
	verify, _ := json.Marshal(model.TwoFactorVerifyRequest{
		ChallengeToken: challengeToken,
		Code:           totpCode(t, setup.Secret, time.Now().Add(30*time.Second)),
	})
	recorder := doRequest(router, http.MethodPost, "/v1/login/2fa", verify, "")
	requireAccountSuspended(t, recorder)
	if refreshCookie(recorder) != "" {
		t.Error("a suspended 2FA login must not set the refresh_token cookie")
	}

	// Y un login nuevo ni siquiera abre un desafío.
	requireAccountSuspended(t, passwordLogin(router, email))
}

// Refresh: con la cookie de un refresh token (ya revocado al suspender) la
// respuesta es 403 ACCOUNT_SUSPENDED, no "token reutilizado".
func TestSuspendedUser_Refresh(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	session := registerAndLoginSession(t, router, "Elena", "elena-susp@example.com", suspendedPassword)

	applySuspension(t, repo, session.UserID, true, 1)

	recorder := doRequestWithRefreshToken(router, http.MethodPost, "/v1/refresh", nil, "", session.RefreshToken)
	requireAccountSuspended(t, recorder)
	if n := activeRefreshTokens(t, db, session.UserID); n != 0 {
		t.Errorf("active refresh tokens = %d, want 0", n)
	}
}

// Un access token todavía vigente deja de servir en cualquier endpoint
// autenticado, y vuelve a servir al reactivar (RequireAuth en e2e consulta la
// base sin caché).
func TestSuspendedUser_ValidAccessTokenIsRejected(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	session := registerAndLoginSession(t, router, "Fede", "fede-susp@example.com", suspendedPassword)

	if rec := doRequest(router, http.MethodGet, "/v1/me/profile", nil, session.AccessToken); rec.Code != http.StatusOK {
		t.Fatalf("profile before suspension status = %d", rec.Code)
	}

	applySuspension(t, repo, session.UserID, true, 1)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/me/profile"},
		{http.MethodPatch, "/v1/me/profile"},
		{http.MethodPost, "/v1/logout"},
	} {
		requireAccountSuspended(t, doRequest(router, tc.method, tc.path, nil, session.AccessToken))
	}

	applySuspension(t, repo, session.UserID, false, 2)
	if rec := doRequest(router, http.MethodGet, "/v1/me/profile", nil, session.AccessToken); rec.Code != http.StatusOK {
		t.Fatalf("profile after reactivation status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// El perfil público de un suspendido es 404, igual que un id inexistente.
func TestSuspendedUser_PublicProfileIsNotFound(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	_, viewerToken := registerAndLogin(t, router, "Gabi", "gabi-viewer@example.com", suspendedPassword)
	target := registerAndLoginSession(t, router, "Hugo", "hugo-susp@example.com", suspendedPassword)

	if rec := doRequest(router, http.MethodGet, "/v1/users/"+target.UserID, nil, viewerToken); rec.Code != http.StatusOK {
		t.Fatalf("public profile before suspension status = %d", rec.Code)
	}

	applySuspension(t, repo, target.UserID, true, 1)
	if rec := doRequest(router, http.MethodGet, "/v1/users/"+target.UserID, nil, viewerToken); rec.Code != http.StatusNotFound {
		t.Fatalf("public profile of a suspended user status = %d, want 404 (body = %s)", rec.Code, rec.Body.String())
	}

	applySuspension(t, repo, target.UserID, false, 2)
	if rec := doRequest(router, http.MethodGet, "/v1/users/"+target.UserID, nil, viewerToken); rec.Code != http.StatusOK {
		t.Fatalf("public profile after reactivation status = %d", rec.Code)
	}
}
