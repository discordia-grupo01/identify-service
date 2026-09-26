package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/utils/twofactor"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func TestTwoFactorFullLifecycle(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	const (
		emailAddress = "2fa-lifecycle@example.com"
		password     = "Secure123"
	)

	session := registerAndLoginSession(t, router, "Ada Lovelace", emailAddress, password)

	setupRecorder := doRequest(router, http.MethodPost, "/v1/me/2fa/setup", nil, session.AccessToken)
	if setupRecorder.Code != http.StatusOK {
		t.Fatalf("setup status = %d, body = %s", setupRecorder.Code, setupRecorder.Body.String())
	}
	var setup model.TwoFactorSetupResponse
	decodeJSON(t, setupRecorder, &setup)
	if setup.Secret == "" || setup.OtpauthURL == "" {
		t.Fatalf("setup response = %+v, want a secret and an otpauth URL", setup)
	}

	if challenge := loginChallenge(t, router, emailAddress, password); challenge != "" {
		t.Fatal("login must not require a second factor before the code is confirmed")
	}

	activateBody, _ := json.Marshal(model.TwoFactorActivateRequest{Code: totpCode(t, setup.Secret, time.Now())})
	activateRecorder := doRequest(router, http.MethodPost, "/v1/me/2fa/activate", activateBody, session.AccessToken)
	if activateRecorder.Code != http.StatusOK {
		t.Fatalf("activate status = %d, body = %s", activateRecorder.Code, activateRecorder.Body.String())
	}
	var codes model.TwoFactorRecoveryCodesResponse
	decodeJSON(t, activateRecorder, &codes)
	if len(codes.RecoveryCodes) != twofactor.RecoveryCodeCount {
		t.Fatalf("recovery codes = %d, want %d", len(codes.RecoveryCodes), twofactor.RecoveryCodeCount)
	}

	statusRecorder := doRequest(router, http.MethodGet, "/v1/me/2fa", nil, session.AccessToken)
	var status model.TwoFactorStatusResponse
	decodeJSON(t, statusRecorder, &status)
	if !status.Enabled || status.RecoveryCodesRemaining != twofactor.RecoveryCodeCount {
		t.Fatalf("status = %+v, want enabled with a full list of recovery codes", status)
	}

	challengeToken := loginChallenge(t, router, emailAddress, password)
	if challengeToken == "" {
		t.Fatal("login with 2FA active must return a challenge")
	}


	wrongBody, _ := json.Marshal(model.TwoFactorVerifyRequest{ChallengeToken: challengeToken, Code: "000000"})
	wrongRecorder := doRequest(router, http.MethodPost, "/v1/login/2fa", wrongBody, "")
	if wrongRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code status = %d, want 401 (body = %s)", wrongRecorder.Code, wrongRecorder.Body.String())
	}
	if refreshCookie(wrongRecorder) != "" {
		t.Fatal("a rejected code must not grant a session")
	}
	verifyBody, _ := json.Marshal(model.TwoFactorVerifyRequest{
		ChallengeToken: challengeToken,
		Code:           totpCode(t, setup.Secret, time.Now().Add(twofactor.Period*time.Second)),
	})
	verifyRecorder := doRequest(router, http.MethodPost, "/v1/login/2fa", verifyBody, "")
	if verifyRecorder.Code != http.StatusOK {
		t.Fatalf("verify status = %d, body = %s", verifyRecorder.Code, verifyRecorder.Body.String())
	}
	var verified model.TwoFactorVerifyResponse
	decodeJSON(t, verifyRecorder, &verified)
	if verified.Token == "" || verified.User.Email != emailAddress {
		t.Fatalf("verify response = %+v, want a session for %s", verified, emailAddress)
	}
	if verified.RecoveryCodeUsed {
		t.Fatal("a TOTP login must not be flagged as a recovery code login")
	}
	if refreshCookie(verifyRecorder) == "" {
		t.Fatal("a completed login must set the refresh_token cookie")
	}

	reusedRecorder := doRequest(router, http.MethodPost, "/v1/login/2fa", verifyBody, "")
	if reusedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("reused challenge status = %d, want 401", reusedRecorder.Code)
	}

	recoveryChallenge := loginChallenge(t, router, emailAddress, password)
	recoveryBody, _ := json.Marshal(model.TwoFactorVerifyRequest{
		ChallengeToken: recoveryChallenge,
		Code:           codes.RecoveryCodes[0],
	})
	recoveryRecorder := doRequest(router, http.MethodPost, "/v1/login/2fa", recoveryBody, "")
	if recoveryRecorder.Code != http.StatusOK {
		t.Fatalf("recovery code status = %d, body = %s", recoveryRecorder.Code, recoveryRecorder.Body.String())
	}
	var recovered model.TwoFactorVerifyResponse
	decodeJSON(t, recoveryRecorder, &recovered)
	if recovered.Token == "" {
		t.Fatal("a recovery code must complete the login")
	}
	if !recovered.RecoveryCodeUsed || recovered.RecoveryCodesRemaining != twofactor.RecoveryCodeCount-1 {
		t.Fatalf("recovery response = %+v, want the used flag and %d remaining",
			recovered, twofactor.RecoveryCodeCount-1)
	}

	reusedChallenge := loginChallenge(t, router, emailAddress, password)
	reusedRecoveryBody, _ := json.Marshal(model.TwoFactorVerifyRequest{
		ChallengeToken: reusedChallenge,
		Code:           codes.RecoveryCodes[0],
	})
	reusedRecoveryRecorder := doRequest(router, http.MethodPost, "/v1/login/2fa", reusedRecoveryBody, "")
	if reusedRecoveryRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("reused recovery code status = %d, want 401", reusedRecoveryRecorder.Code)
	}

	wrongPasswordBody, _ := json.Marshal(model.TwoFactorPasswordRequest{Password: "Incorrecta1"})
	wrongPasswordRecorder := doRequest(router, http.MethodPost, "/v1/me/2fa/disable", wrongPasswordBody, session.AccessToken)
	if wrongPasswordRecorder.Code != http.StatusForbidden {
		t.Fatalf("disable with a wrong password status = %d, want 403", wrongPasswordRecorder.Code)
	}

	disableBody, _ := json.Marshal(model.TwoFactorPasswordRequest{Password: password})
	disableRecorder := doRequest(router, http.MethodPost, "/v1/me/2fa/disable", disableBody, session.AccessToken)
	if disableRecorder.Code != http.StatusNoContent {
		t.Fatalf("disable status = %d, body = %s", disableRecorder.Code, disableRecorder.Body.String())
	}

	if challenge := loginChallenge(t, router, emailAddress, password); challenge != "" {
		t.Fatal("login must not require a second factor after disabling it")
	}
}

func TestTwoFactorRegenerateInvalidatesTheOldList(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	const (
		emailAddress = "2fa-regenerate@example.com"
		password     = "Secure123"
	)

	session := registerAndLoginSession(t, router, "Grace Hopper", emailAddress, password)
	oldCodes := activateTwoFactor(t, router, session.AccessToken)

	regenerateBody, _ := json.Marshal(model.TwoFactorPasswordRequest{Password: password})
	regenerateRecorder := doRequest(router, http.MethodPost, "/v1/me/2fa/recovery-codes", regenerateBody, session.AccessToken)
	if regenerateRecorder.Code != http.StatusOK {
		t.Fatalf("regenerate status = %d, body = %s", regenerateRecorder.Code, regenerateRecorder.Body.String())
	}
	var newCodes model.TwoFactorRecoveryCodesResponse
	decodeJSON(t, regenerateRecorder, &newCodes)

	oldBody, _ := json.Marshal(model.TwoFactorVerifyRequest{
		ChallengeToken: loginChallenge(t, router, emailAddress, password),
		Code:           oldCodes[0],
	})
	if recorder := doRequest(router, http.MethodPost, "/v1/login/2fa", oldBody, ""); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("old recovery code status = %d, want 401", recorder.Code)
	}

	newBody, _ := json.Marshal(model.TwoFactorVerifyRequest{
		ChallengeToken: loginChallenge(t, router, emailAddress, password),
		Code:           newCodes.RecoveryCodes[0],
	})
	if recorder := doRequest(router, http.MethodPost, "/v1/login/2fa", newBody, ""); recorder.Code != http.StatusOK {
		t.Fatalf("new recovery code status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func loginChallenge(t *testing.T, router *gin.Engine, emailAddress string, password string) string {
	t.Helper()

	body, _ := json.Marshal(model.LoginRequest{Email: emailAddress, Password: password})
	recorder := doRequest(router, http.MethodPost, "/v1/login", body, "")
	if recorder.Code != http.StatusOK && recorder.Code != http.StatusAccepted {
		t.Fatalf("login status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Code == http.StatusOK {
		return ""
	}

	var challenge model.TwoFactorChallengeResponse
	decodeJSON(t, recorder, &challenge)
	if challenge.ChallengeToken == "" {
		t.Fatal("a two factor challenge must carry a token")
	}
	return challenge.ChallengeToken
}

func activateTwoFactor(t *testing.T, router *gin.Engine, accessToken string) []string {
	t.Helper()

	setupRecorder := doRequest(router, http.MethodPost, "/v1/me/2fa/setup", nil, accessToken)
	if setupRecorder.Code != http.StatusOK {
		t.Fatalf("setup status = %d, body = %s", setupRecorder.Code, setupRecorder.Body.String())
	}
	var setup model.TwoFactorSetupResponse
	decodeJSON(t, setupRecorder, &setup)

	activateBody, _ := json.Marshal(model.TwoFactorActivateRequest{Code: totpCode(t, setup.Secret, time.Now())})
	activateRecorder := doRequest(router, http.MethodPost, "/v1/me/2fa/activate", activateBody, accessToken)
	if activateRecorder.Code != http.StatusOK {
		t.Fatalf("activate status = %d, body = %s", activateRecorder.Code, activateRecorder.Body.String())
	}
	var codes model.TwoFactorRecoveryCodesResponse
	decodeJSON(t, activateRecorder, &codes)
	return codes.RecoveryCodes
}

// totpCode calcula el código que mostraría la app autenticadora en ese
// instante, a partir del secreto que entregó el backend.
func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
		Period:    twofactor.Period,
		Skew:      0,
		Digits:    twofactor.Digits,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("GenerateCodeCustom() error = %v", err)
	}
	return code
}

func refreshCookie(recorder *httptest.ResponseRecorder) string {
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == "refresh_token" {
			return cookie.Value
		}
	}
	return ""
}
