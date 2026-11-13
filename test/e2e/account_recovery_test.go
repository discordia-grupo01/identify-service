package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/discordia-grupo01/events-contract/admin"
	"github.com/discordia-grupo01/events-contract/envelope"
	"github.com/discordia-grupo01/events-contract/users"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

func recoveryDecision(t *testing.T, requestID, userID string, approved bool) (string, []byte) {
	t.Helper()
	meta := envelope.Meta{EventID: uuid.NewString(), OccurredAt: time.Now().UTC().Truncate(time.Second)}
	if approved {
		event := admin.AccountRecoveryApproved{Meta: meta, RequestID: requestID, UserID: userID}
		event.Sign(adminE2ESecret)
		return admin.RoutingKeyAccountRecoveryApproved, mustMarshal(t, event)
	}
	event := admin.AccountRecoveryRejected{Meta: meta, RequestID: requestID, UserID: userID}
	event.Sign(adminE2ESecret)
	return admin.RoutingKeyAccountRecoveryRejected, mustMarshal(t, event)
}

func requestAccountRecovery(t *testing.T, router *gin.Engine, challenge, message string) (int, model.AccountRecoveryCreateResponse) {
	t.Helper()
	body, _ := json.Marshal(model.AccountRecoveryCreateRequest{ChallengeToken: challenge, Message: message})
	recorder := doRequest(router, http.MethodPost, "/v1/login/2fa/recovery-request", body, "")
	var res model.AccountRecoveryCreateResponse
	if recorder.Code == http.StatusCreated {
		decodeJSON(t, recorder, &res)
	}
	return recorder.Code, res
}

// El usuario perdió la app y los códigos: pide la recuperación desde el paso de
// verificación, el staff la aprueba y vuelve a entrar sin segundo factor.
func TestAccountRecoveryApprovedTurnsTwoFactorOff(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	const emailAddress, password = "ada.recovery@example.com", "Secure123"
	userID, accessToken := registerAndLogin(t, router, "Ada", emailAddress, password)
	activateTwoFactor(t, router, accessToken)

	challenge := loginChallenge(t, router, emailAddress, password)
	status, created := requestAccountRecovery(t, router, challenge, "cambié de celular")
	if status != http.StatusCreated {
		t.Fatalf("recovery request status = %d, want %d", status, http.StatusCreated)
	}
	if created.Status != model.AccountRecoveryPending || created.EmailHint != "a***y@example.com" {
		t.Errorf("response = %+v", created)
	}

	// Una sola pendiente por usuario.
	if status, _ := requestAccountRecovery(t, router, loginChallenge(t, router, emailAddress, password), ""); status != http.StatusConflict {
		t.Fatalf("second request status = %d, want %d", status, http.StatusConflict)
	}

	var routingKey string
	var payload []byte
	if err := db.QueryRow(`SELECT routing_key, payload FROM outbox_events WHERE routing_key = $1`, users.RoutingKeyAccountRecoveryRequested).Scan(&routingKey, &payload); err != nil {
		t.Fatalf("outbox event: %v", err)
	}
	var event users.AccountRecoveryRequested
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	if event.RequestID != created.RequestID || event.UserID != userID || event.Message != "cambié de celular" {
		t.Errorf("outbox event = %+v", event)
	}

	deps := accountRecoveryFor(t, router)
	consumer := newAdminConsumer(db)
	consumer.HandleAccountRecovery(deps.service)
	key, body := recoveryDecision(t, created.RequestID, userID, true)
	if consumer.Process(context.Background(), key, body) {
		t.Fatal("Process() asked to requeue the approval")
	}

	// Sin 2FA, el login vuelve a dar sesión directo.
	loginBody, _ := json.Marshal(model.LoginRequest{Email: emailAddress, Password: password})
	login := doRequest(router, http.MethodPost, "/v1/login", loginBody, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login after approval status = %d, body = %s", login.Code, login.Body.String())
	}
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM two_factor_recovery_codes WHERE user_id = $1`, userID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Errorf("recovery codes left = %d, want 0", remaining)
	}
	sent := deps.mailer.all()
	if len(sent) != 1 || !sent[0].Approved || sent[0].Email != emailAddress {
		t.Errorf("sent = %+v, want one approval to %s", sent, emailAddress)
	}

	// Un evento repetido no cambia nada ni manda otro mail.
	if consumer.Process(context.Background(), key, body) {
		t.Fatal("Process() asked to requeue a repeated approval")
	}
	if len(deps.mailer.all()) != 1 {
		t.Error("a repeated approval must not send another email")
	}
}

func TestAccountRecoveryRejectedKeepsTwoFactorAndAllowsANewRequest(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	const emailAddress, password = "bruno.recovery@example.com", "Secure123"
	userID, accessToken := registerAndLogin(t, router, "Bruno", emailAddress, password)
	activateTwoFactor(t, router, accessToken)

	status, created := requestAccountRecovery(t, router, loginChallenge(t, router, emailAddress, password), "")
	if status != http.StatusCreated {
		t.Fatalf("recovery request status = %d", status)
	}

	deps := accountRecoveryFor(t, router)
	consumer := newAdminConsumer(db)
	consumer.HandleAccountRecovery(deps.service)
	key, body := recoveryDecision(t, created.RequestID, userID, false)
	if consumer.Process(context.Background(), key, body) {
		t.Fatal("Process() asked to requeue the rejection")
	}

	// El 2FA sigue: el login vuelve a pedir el segundo factor.
	challenge := loginChallenge(t, router, emailAddress, password)

	// Ya no hay una pendiente, así que puede volver a pedirla.
	if status, _ := requestAccountRecovery(t, router, challenge, "otra vez"); status != http.StatusCreated {
		t.Fatalf("new request after rejection status = %d, want %d", status, http.StatusCreated)
	}
	sent := deps.mailer.all()
	if len(sent) != 1 || sent[0].Approved {
		t.Errorf("sent = %+v, want one rejection", sent)
	}
}

func TestAccountRecoveryApprovalForAnotherUserChangesNothing(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	const emailAddress, password = "carla.recovery@example.com", "Secure123"
	_, accessToken := registerAndLogin(t, router, "Carla", emailAddress, password)
	activateTwoFactor(t, router, accessToken)

	_, created := requestAccountRecovery(t, router, loginChallenge(t, router, emailAddress, password), "")
	otherUserID, _ := registerAndLogin(t, router, "Dani", "dani.recovery@example.com", password)

	deps := accountRecoveryFor(t, router)
	consumer := newAdminConsumer(db)
	consumer.HandleAccountRecovery(deps.service)
	key, body := recoveryDecision(t, created.RequestID, otherUserID, true)
	if consumer.Process(context.Background(), key, body) {
		t.Fatal("Process() asked to requeue")
	}

	loginChallenge(t, router, emailAddress, password)
	if len(deps.mailer.all()) != 0 {
		t.Error("no debería mandar mail")
	}
}

func TestAccountRecoveryRequiresAValidChallenge(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	if status, _ := requestAccountRecovery(t, router, "no-existe", ""); status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", status, http.StatusUnauthorized)
	}
}
