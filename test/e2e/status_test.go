package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

// TestStatusEndToEnd cubre HU 38 (Estado personalizado) de punta a punta:
// fijar el estado, verificar que se refleje en el perfil propio y en el
// perfil público visto por otro usuario, y después limpiarlo.
func TestStatusEndToEnd(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	aliceID, aliceToken := registerAndLogin(t, router, "Alice", "alice@example.com", "Passw0rd!")
	_, bobToken := registerAndLogin(t, router, "Bob", "bob@example.com", "Passw0rd!")

	t.Run("fija el estado y se refleja en el perfil propio y en el público", func(t *testing.T) {
		putRecorder := doRequest(router, http.MethodPut, "/v1/me/status", []byte(`{"status_text":"Jugando","status_emoji":"🎮"}`), aliceToken)
		if putRecorder.Code != http.StatusOK {
			t.Fatalf("PUT status = %d, body = %s", putRecorder.Code, putRecorder.Body.String())
		}

		ownProfileRecorder := doRequest(router, http.MethodGet, "/v1/me/profile", nil, aliceToken)
		var ownProfile model.UserResponse
		decodeJSON(t, ownProfileRecorder, &ownProfile)
		if ownProfile.StatusText != "Jugando" || ownProfile.StatusEmoji != "🎮" {
			t.Fatalf("own profile status = %+v", ownProfile)
		}

		publicProfileRecorder := doRequest(router, http.MethodGet, "/v1/users/"+aliceID, nil, bobToken)
		var publicProfile model.PublicUserResponse
		decodeJSON(t, publicProfileRecorder, &publicProfile)
		if publicProfile.StatusText != "Jugando" || publicProfile.StatusEmoji != "🎮" {
			t.Fatalf("public profile status = %+v", publicProfile)
		}
	})

	t.Run("actualiza solo el campo enviado, sin tocar el otro", func(t *testing.T) {
		recorder := doRequest(router, http.MethodPut, "/v1/me/status", []byte(`{"status_text":"Reunión"}`), aliceToken)
		if recorder.Code != http.StatusOK {
			t.Fatalf("PUT status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var response model.UserResponse
		decodeJSON(t, recorder, &response)
		if response.StatusText != "Reunión" || response.StatusEmoji != "🎮" {
			t.Fatalf("unexpected response: %+v", response)
		}
	})

	t.Run("rechaza un body sin cambios", func(t *testing.T) {
		recorder := doRequest(router, http.MethodPut, "/v1/me/status", []byte(`{}`), aliceToken)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
		}
	})

	t.Run("rechaza texto demasiado largo", func(t *testing.T) {
		longText, err := json.Marshal(map[string]string{"status_text": strings.Repeat("a", 200)})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		recorder := doRequest(router, http.MethodPut, "/v1/me/status", longText, aliceToken)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("devuelve 401 sin token", func(t *testing.T) {
		recorder := doRequest(router, http.MethodPut, "/v1/me/status", []byte(`{"status_text":"Jugando"}`), "")
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
		}
	})

	t.Run("limpia el estado y es idempotente", func(t *testing.T) {
		firstDelete := doRequest(router, http.MethodDelete, "/v1/me/status", nil, aliceToken)
		if firstDelete.Code != http.StatusNoContent {
			t.Fatalf("DELETE status = %d, body = %s", firstDelete.Code, firstDelete.Body.String())
		}

		profileRecorder := doRequest(router, http.MethodGet, "/v1/me/profile", nil, aliceToken)
		var profile model.UserResponse
		decodeJSON(t, profileRecorder, &profile)
		if profile.StatusText != "" || profile.StatusEmoji != "" {
			t.Fatalf("status was not cleared: %+v", profile)
		}

		secondDelete := doRequest(router, http.MethodDelete, "/v1/me/status", nil, aliceToken)
		if secondDelete.Code != http.StatusNoContent {
			t.Fatalf("second DELETE status = %d, want %d (idempotent)", secondDelete.Code, http.StatusNoContent)
		}
	})

	t.Run("actualizaciones concurrentes de campos distintos no se pisan entre sí", func(t *testing.T) {
		_, carolToken := registerAndLogin(t, router, "Carol", "carol@example.com", "Passw0rd!")

		for i := 0; i < 10; i++ {
			// Reset a un estado conocido antes de cada intento.
			doRequest(router, http.MethodDelete, "/v1/me/status", nil, carolToken)

			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				doRequest(router, http.MethodPut, "/v1/me/status", []byte(`{"status_text":"A"}`), carolToken)
			}()
			go func() {
				defer wg.Done()
				doRequest(router, http.MethodPut, "/v1/me/status", []byte(`{"status_emoji":"B"}`), carolToken)
			}()
			wg.Wait()

			recorder := doRequest(router, http.MethodGet, "/v1/me/profile", nil, carolToken)
			var profile model.UserResponse
			decodeJSON(t, recorder, &profile)
			if profile.StatusText != "A" || profile.StatusEmoji != "B" {
				t.Fatalf("attempt %d: concurrent updates clobbered each other, got status_text=%q status_emoji=%q, want both set", i, profile.StatusText, profile.StatusEmoji)
			}
		}
	})
}
