package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

func TestEmailConfirmationEndToEnd(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	emailAddress := "ada@example.com"
	password := "Passw0rd!"

	registerBody, _ := json.Marshal(model.RegisterRequest{
		Name:     "Ada",
		Email:    emailAddress,
		Password: password,
	})
	registerRecorder := doRequest(router, http.MethodPost, "/v1/users", registerBody, "")
	if registerRecorder.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", registerRecorder.Code, registerRecorder.Body.String())
	}

	var registered model.UserResponse
	decodeJSON(t, registerRecorder, &registered)
	if registered.EmailVerified {
		t.Fatal("newly registered user must not be verified before confirmation")
	}

	loginBody, _ := json.Marshal(model.LoginRequest{Email: emailAddress, Password: password})
	loginRecorder := doRequest(router, http.MethodPost, "/v1/login", loginBody, "")
	if loginRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unconfirmed login status = %d, body = %s", loginRecorder.Code, loginRecorder.Body.String())
	}

	mailer := confirmationMailerFor(t, router)
	firstEmail, ok := mailer.latest(emailAddress)
	if !ok {
		t.Fatal("registration did not send a confirmation email")
	}
	firstToken := confirmationToken(t, firstEmail)

	requestBody, _ := json.Marshal(model.EmailConfirmationRequest{Email: emailAddress})
	resendRecorder := doRequest(router, http.MethodPost, "/v1/email-confirmation", requestBody, "")
	if resendRecorder.Code != http.StatusAccepted {
		t.Fatalf("resend status = %d, body = %s", resendRecorder.Code, resendRecorder.Body.String())
	}

	secondEmail, ok := mailer.latest(emailAddress)
	if !ok {
		t.Fatal("resend did not send a confirmation email")
	}
	secondToken := confirmationToken(t, secondEmail)
	if firstToken == secondToken {
		t.Fatal("resend must issue a new confirmation token")
	}

	oldTokenBody, _ := json.Marshal(model.EmailConfirmationTokenRequest{Token: firstToken})
	oldTokenRecorder := doRequest(router, http.MethodPost, "/v1/email-confirmation/confirm", oldTokenBody, "")
	if oldTokenRecorder.Code != http.StatusBadRequest {
		t.Fatalf("old token confirmation status = %d, body = %s", oldTokenRecorder.Code, oldTokenRecorder.Body.String())
	}

	newTokenBody, _ := json.Marshal(model.EmailConfirmationTokenRequest{Token: secondToken})
	newTokenRecorder := doRequest(router, http.MethodPost, "/v1/email-confirmation/confirm", newTokenBody, "")
	if newTokenRecorder.Code != http.StatusOK {
		t.Fatalf("new token confirmation status = %d, body = %s", newTokenRecorder.Code, newTokenRecorder.Body.String())
	}

	consumedTokenRecorder := doRequest(router, http.MethodPost, "/v1/email-confirmation/confirm", newTokenBody, "")
	if consumedTokenRecorder.Code != http.StatusBadRequest {
		t.Fatalf("consumed token confirmation status = %d, body = %s", consumedTokenRecorder.Code, consumedTokenRecorder.Body.String())
	}

	loginRecorder = doRequest(router, http.MethodPost, "/v1/login", loginBody, "")
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("confirmed login status = %d, body = %s", loginRecorder.Code, loginRecorder.Body.String())
	}
}
