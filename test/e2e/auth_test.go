package e2e

import (
	"net/http"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

func TestRefreshTokenEndToEnd(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	session := registerAndLoginSession(
		t,
		router,
		"Alice",
		"alice@example.com",
		"Passw0rd!",
	)

	refreshRecorder := doRequestWithRefreshToken(
		router,
		http.MethodPost,
		"/v1/refresh",
		nil,
		"",
		session.RefreshToken,
	)

	if refreshRecorder.Code != http.StatusOK {
		t.Fatalf(
			"refresh status = %d, body = %s",
			refreshRecorder.Code,
			refreshRecorder.Body.String(),
		)
	}

	var refreshed model.AuthResponse
	decodeJSON(t, refreshRecorder, &refreshed)

	if refreshed.Token == "" {
		t.Fatal("refresh response did not contain a new access token")
	}

	rotatedRefreshToken := refreshCookieValue(t, refreshRecorder)

	if rotatedRefreshToken == session.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}

	// El nuevo refresh token todavía es válido.
	logoutRecorder := doRequestWithRefreshToken(
		router,
		http.MethodPost,
		"/v1/logout",
		nil,
		refreshed.Token,
		rotatedRefreshToken,
	)

	if logoutRecorder.Code != http.StatusNoContent {
		t.Fatalf(
			"logout status = %d, want %d",
			logoutRecorder.Code,
			http.StatusNoContent,
		)
	}

	// Después del logout, el refresh token ya no puede utilizarse.
	afterLogoutRecorder := doRequestWithRefreshToken(
		router,
		http.MethodPost,
		"/v1/refresh",
		nil,
		"",
		rotatedRefreshToken,
	)

	if afterLogoutRecorder.Code != http.StatusUnauthorized {
		t.Fatalf(
			"refresh after logout status = %d, want %d",
			afterLogoutRecorder.Code,
			http.StatusUnauthorized,
		)
	}
}

func TestRefreshTokenReuseRevokesFamily(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	session := registerAndLoginSession(
		t,
		router,
		"Alice",
		"alice@example.com",
		"Passw0rd!",
	)

	refreshRecorder := doRequestWithRefreshToken(
		router,
		http.MethodPost,
		"/v1/refresh",
		nil,
		"",
		session.RefreshToken,
	)

	if refreshRecorder.Code != http.StatusOK {
		t.Fatalf(
			"refresh status = %d, body = %s",
			refreshRecorder.Code,
			refreshRecorder.Body.String(),
		)
	}

	rotatedRefreshToken := refreshCookieValue(t, refreshRecorder)

	// El token original ya fue rotado y, por lo tanto, está revocado.
	reusedRecorder := doRequestWithRefreshToken(
		router,
		http.MethodPost,
		"/v1/refresh",
		nil,
		"",
		session.RefreshToken,
	)

	if reusedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf(
			"reused refresh status = %d, want %d",
			reusedRecorder.Code,
			http.StatusUnauthorized,
		)
	}

	// Como hubo reutilización, toda la familia quedó revocada.
	afterReuseRecorder := doRequestWithRefreshToken(
		router,
		http.MethodPost,
		"/v1/refresh",
		nil,
		"",
		rotatedRefreshToken,
	)

	if afterReuseRecorder.Code != http.StatusUnauthorized {
		t.Fatalf(
			"refresh after reuse status = %d, want %d",
			afterReuseRecorder.Code,
			http.StatusUnauthorized,
		)
	}
}

func refreshCookieValue(t *testing.T, recorder interface{ Result() *http.Response }) string {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == "refresh_token" && cookie.Value != "" {
			return cookie.Value
		}
	}
	t.Fatal("response did not set a refresh_token cookie")
	return ""
}
