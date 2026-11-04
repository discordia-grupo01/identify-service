package e2e

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
)

func premiumState(t *testing.T, db *sql.DB, userID string) (active bool, version int64) {
	t.Helper()
	if err := db.QueryRow(`SELECT premium_active, premium_version FROM users WHERE id = $1`, userID).Scan(&active, &version); err != nil {
		t.Fatalf("read premium state: %v", err)
	}
	return active, version
}

func requirePremiumResult(t *testing.T, got, want repository.PremiumResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("ApplyPremium error = %v", err)
	}
	if got != want {
		t.Fatalf("ApplyPremium result = %v, want %v", got, want)
	}
}

// Un usuario nuevo no es Premium; aplicar un evento lo activa y otro con
// versión mayor lo desactiva.
func TestApplyPremium_ActivatesAndDeactivates(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	ctx := context.Background()
	user := registerAndLoginSession(t, router, "Nora", "nora@example.com", "Passw0rd!")

	if active, version := premiumState(t, db, user.UserID); active || version != 0 {
		t.Fatalf("new user: premium_active %v, version %d; want false, 0", active, version)
	}

	result, err := repo.ApplyPremium(ctx, user.UserID, true, 1)
	requirePremiumResult(t, result, repository.PremiumApplied, err)
	if active, version := premiumState(t, db, user.UserID); !active || version != 1 {
		t.Errorf("after activation: premium_active %v, version %d; want true, 1", active, version)
	}

	result, err = repo.ApplyPremium(ctx, user.UserID, false, 2)
	requirePremiumResult(t, result, repository.PremiumApplied, err)
	if active, version := premiumState(t, db, user.UserID); active || version != 2 {
		t.Errorf("after expiry: premium_active %v, version %d; want false, 2", active, version)
	}
}

// Un duplicado o un evento más viejo no pisa uno más nuevo.
func TestApplyPremium_IgnoresStaleEvents(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	ctx := context.Background()
	user := registerAndLoginSession(t, router, "Omar", "omar@example.com", "Passw0rd!")

	result, err := repo.ApplyPremium(ctx, user.UserID, true, 3)
	requirePremiumResult(t, result, repository.PremiumApplied, err)

	for _, version := range []int64{3, 2} {
		result, err := repo.ApplyPremium(ctx, user.UserID, false, version)
		requirePremiumResult(t, result, repository.PremiumStale, err)
	}
	if active, version := premiumState(t, db, user.UserID); !active || version != 3 {
		t.Errorf("premium_active %v, version %d; want true, 3", active, version)
	}
}

func TestApplyPremium_UnknownUser(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewUserRepository(db)

	result, err := repo.ApplyPremium(context.Background(), "99999999-2222-3333-4444-555555555555", true, 1)
	requirePremiumResult(t, result, repository.PremiumUserNotFound, err)
}

// El perfil propio y el público muestran premium.
func TestProfilesShowPremium(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	paying := registerAndLoginSession(t, router, "Pia", "pia@example.com", "Passw0rd!")
	viewer := registerAndLoginSession(t, router, "Quique", "quique@example.com", "Passw0rd!")

	result, err := repo.ApplyPremium(context.Background(), paying.UserID, true, 1)
	requirePremiumResult(t, result, repository.PremiumApplied, err)

	own := doRequest(router, http.MethodGet, "/v1/me/profile", nil, paying.AccessToken)
	if own.Code != http.StatusOK {
		t.Fatalf("GET /v1/me/profile status = %d, body = %s", own.Code, own.Body.String())
	}
	var ownProfile model.UserResponse
	decodeJSON(t, own, &ownProfile)
	if !ownProfile.Premium {
		t.Errorf("own profile premium = false, want true: %s", own.Body.String())
	}

	public := doRequest(router, http.MethodGet, "/v1/users/"+paying.UserID, nil, viewer.AccessToken)
	if public.Code != http.StatusOK {
		t.Fatalf("GET /v1/users/:id status = %d, body = %s", public.Code, public.Body.String())
	}
	var publicProfile model.PublicUserResponse
	decodeJSON(t, public, &publicProfile)
	if !publicProfile.Premium {
		t.Errorf("public profile premium = false, want true: %s", public.Body.String())
	}

	viewerOwn := doRequest(router, http.MethodGet, "/v1/me/profile", nil, viewer.AccessToken)
	var viewerProfile model.UserResponse
	decodeJSON(t, viewerOwn, &viewerProfile)
	if viewerProfile.Premium {
		t.Error("a user without Premium should show premium = false")
	}
}
