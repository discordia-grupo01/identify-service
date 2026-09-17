package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

// TestPublicProfileEndToEnd cubre HU 36 (Visualización de perfil público) de
// punta a punta: dos usuarios reales, login real, y la consulta del perfil
// público de uno desde el otro, contra Postgres real.
func TestPublicProfileEndToEnd(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	_, aliceToken := registerAndLogin(t, router, "Alice", "alice@example.com", "Passw0rd!")
	bobID, _ := registerAndLogin(t, router, "Bob", "bob@example.com", "Passw0rd!")

	t.Run("devuelve el perfil público del otro usuario sin datos privados", func(t *testing.T) {
		recorder := doRequest(router, http.MethodGet, "/v1/users/"+bobID, nil, aliceToken)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}

		rawBytes := append([]byte(nil), recorder.Body.Bytes()...)

		var publicProfile model.PublicUserResponse
		if err := json.Unmarshal(rawBytes, &publicProfile); err != nil {
			t.Fatalf("unmarshal public profile: %v (body = %s)", err, rawBytes)
		}
		if publicProfile.ID != bobID || publicProfile.Name != "Bob" {
			t.Fatalf("unexpected public profile: %+v", publicProfile)
		}

		var rawBody map[string]any
		if err := json.Unmarshal(rawBytes, &rawBody); err != nil {
			t.Fatalf("unmarshal raw body: %v", err)
		}
		if _, hasEmail := rawBody["email"]; hasEmail {
			t.Fatal("public profile must not expose email")
		}
		if _, hasDescription := rawBody["description"]; hasDescription {
			t.Fatal("public profile must not expose description")
		}
	})

	t.Run("devuelve 401 sin token", func(t *testing.T) {
		recorder := doRequest(router, http.MethodGet, "/v1/users/"+bobID, nil, "")
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
		}
	})

	t.Run("devuelve 404 para un id inexistente", func(t *testing.T) {
		recorder := doRequest(router, http.MethodGet, "/v1/users/00000000-0000-0000-0000-000000000000", nil, aliceToken)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("devuelve 400 para un id mal formado", func(t *testing.T) {
		recorder := doRequest(router, http.MethodGet, "/v1/users/not-a-uuid", nil, aliceToken)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
	})
}
