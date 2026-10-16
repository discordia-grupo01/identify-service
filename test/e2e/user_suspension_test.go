package e2e

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/repository"
)

func suspensionState(t *testing.T, db *sql.DB, userID string) (suspendedAt sql.NullTime, version int64) {
	t.Helper()
	if err := db.QueryRow(`SELECT suspended_at, suspension_version FROM users WHERE id = $1`, userID).Scan(&suspendedAt, &version); err != nil {
		t.Fatalf("read suspension state: %v", err)
	}
	return suspendedAt, version
}

func activeRefreshTokens(t *testing.T, db *sql.DB, userID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL`, userID).Scan(&n); err != nil {
		t.Fatalf("count refresh tokens: %v", err)
	}
	return n
}

func requireResult(t *testing.T, got, want repository.SuspensionResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("ApplySuspension error = %v", err)
	}
	if got != want {
		t.Fatalf("ApplySuspension result = %v, want %v", got, want)
	}
}

// Suspender guarda suspended_at (el occurred_at del evento) y la versión, y
// revoca todos los refresh tokens del usuario; los de otros no se tocan.
func TestApplySuspension_SuspendsAndRevokesRefreshTokens(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	ctx := context.Background()
	victim := registerAndLoginSession(t, router, "Judy", "judy@example.com", "Passw0rd!")
	other := registerAndLoginSession(t, router, "Karl", "karl@example.com", "Passw0rd!")
	if activeRefreshTokens(t, db, victim.UserID) == 0 {
		t.Fatal("precondition: the user should have an active refresh token")
	}

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	result, err := repo.ApplySuspension(ctx, victim.UserID, true, at, 1)
	requireResult(t, result, repository.SuspensionApplied, err)

	suspendedAt, version := suspensionState(t, db, victim.UserID)
	if !suspendedAt.Valid || !suspendedAt.Time.Equal(at) || version != 1 {
		t.Errorf("state = suspended_at %v (valid %v), version %d; want %v, 1", suspendedAt.Time, suspendedAt.Valid, version, at)
	}
	if n := activeRefreshTokens(t, db, victim.UserID); n != 0 {
		t.Errorf("active refresh tokens = %d, want 0", n)
	}
	if n := activeRefreshTokens(t, db, other.UserID); n == 0 {
		t.Error("another user's refresh tokens were revoked")
	}
	if suspended, err := repo.IsSuspended(ctx, victim.UserID); err != nil || !suspended {
		t.Errorf("IsSuspended = %v, %v; want true", suspended, err)
	}
	if suspended, err := repo.IsSuspended(ctx, other.UserID); err != nil || suspended {
		t.Errorf("IsSuspended(other) = %v, %v; want false", suspended, err)
	}
}

// Reactivar limpia suspended_at y sube la versión.
func TestApplySuspension_Reactivates(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	ctx := context.Background()
	user := registerAndLoginSession(t, router, "Liam", "liam@example.com", "Passw0rd!")
	now := time.Now().UTC()

	result, err := repo.ApplySuspension(ctx, user.UserID, true, now, 1)
	requireResult(t, result, repository.SuspensionApplied, err)
	result, err = repo.ApplySuspension(ctx, user.UserID, false, now, 2)
	requireResult(t, result, repository.SuspensionApplied, err)

	suspendedAt, version := suspensionState(t, db, user.UserID)
	if suspendedAt.Valid || version != 2 {
		t.Errorf("state = suspended_at valid %v, version %d; want NULL, 2", suspendedAt.Valid, version)
	}
	if suspended, err := repo.IsSuspended(ctx, user.UserID); err != nil || suspended {
		t.Errorf("IsSuspended = %v, %v; want false", suspended, err)
	}
}

// Un duplicado o un evento con versión menor no cambia nada.
func TestApplySuspension_StaleVersionIsANoOp(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	ctx := context.Background()
	user := registerAndLoginSession(t, router, "Mia", "mia@example.com", "Passw0rd!")
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	result, err := repo.ApplySuspension(ctx, user.UserID, true, at, 5)
	requireResult(t, result, repository.SuspensionApplied, err)

	for _, tc := range []struct {
		name      string
		suspended bool
		version   int64
	}{
		{"duplicate", true, 5},
		{"older reactivation", false, 3},
		{"older suspension", true, 1},
	} {
		result, err := repo.ApplySuspension(ctx, user.UserID, tc.suspended, at.Add(time.Hour), tc.version)
		if err != nil || result != repository.SuspensionStale {
			t.Errorf("%s: result = %v, err = %v; want SuspensionStale", tc.name, result, err)
		}
	}
	suspendedAt, version := suspensionState(t, db, user.UserID)
	if !suspendedAt.Valid || !suspendedAt.Time.Equal(at) || version != 5 {
		t.Errorf("state = %v (valid %v), version %d; want %v, 5 (unchanged)", suspendedAt.Time, suspendedAt.Valid, version, at)
	}
}

// Suspensión y reactivación llegando al revés: queda el estado del evento
// más nuevo y la suspensión vieja no revoca nada.
func TestApplySuspension_OutOfOrderKeepsTheNewestEvent(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	ctx := context.Background()
	user := registerAndLoginSession(t, router, "Noah", "noah@example.com", "Passw0rd!")
	now := time.Now().UTC()

	// Llega primero la reactivación (v2) y después la suspensión (v1).
	result, err := repo.ApplySuspension(ctx, user.UserID, false, now, 2)
	requireResult(t, result, repository.SuspensionApplied, err)
	result, err = repo.ApplySuspension(ctx, user.UserID, true, now, 1)
	requireResult(t, result, repository.SuspensionStale, err)

	suspendedAt, version := suspensionState(t, db, user.UserID)
	if suspendedAt.Valid || version != 2 {
		t.Errorf("state = suspended_at valid %v, version %d; want active at version 2", suspendedAt.Valid, version)
	}
	if n := activeRefreshTokens(t, db, user.UserID); n == 0 {
		t.Error("a stale suspension must not revoke refresh tokens")
	}
}

func TestApplySuspension_UnknownUser(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewUserRepository(db)
	ctx := context.Background()
	const ghost = "00000000-0000-0000-0000-000000000000"

	result, err := repo.ApplySuspension(ctx, ghost, true, time.Now(), 1)
	requireResult(t, result, repository.SuspensionUserNotFound, err)
	if suspended, err := repo.IsSuspended(ctx, ghost); err != nil || suspended {
		t.Errorf("IsSuspended(unknown) = %v, %v; want false, nil", suspended, err)
	}
}

// La suspensión y la revocación de tokens son una sola transacción: si falla
// la revocación, el usuario no queda suspendido a medias.
func TestApplySuspension_IsAtomicWithTokenRevocation(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	ctx := context.Background()
	user := registerAndLoginSession(t, router, "Olga", "olga@example.com", "Passw0rd!")

	if _, err := db.Exec(`
		CREATE OR REPLACE FUNCTION reject_token_revocation() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'revocacion rechazada por el test'; END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER reject_token_revocation BEFORE UPDATE ON refresh_tokens
		FOR EACH ROW EXECUTE FUNCTION reject_token_revocation()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP TRIGGER IF EXISTS reject_token_revocation ON refresh_tokens; DROP FUNCTION IF EXISTS reject_token_revocation()`)
	})

	if _, err := repo.ApplySuspension(ctx, user.UserID, true, time.Now().UTC(), 1); err == nil {
		t.Fatal("ApplySuspension error = nil, want the revocation failure")
	}
	suspendedAt, version := suspensionState(t, db, user.UserID)
	if suspendedAt.Valid || version != 0 {
		t.Errorf("state = suspended_at valid %v, version %d; want untouched (rollback)", suspendedAt.Valid, version)
	}
	if n := activeRefreshTokens(t, db, user.UserID); n == 0 {
		t.Error("refresh tokens were revoked despite the rollback")
	}
}

// Eventos concurrentes del mismo usuario se serializan por el lock de la
// fila: gana siempre la versión mayor, sea cual sea el orden de llegada.
func TestApplySuspension_ConcurrentEventsKeepHighestVersion(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	repo := repository.NewUserRepository(db)
	ctx := context.Background()
	user := registerAndLoginSession(t, router, "Pablo", "pablo@example.com", "Passw0rd!")
	now := time.Now().UTC()

	var wg sync.WaitGroup
	for version := int64(1); version <= 8; version++ {
		wg.Add(1)
		go func(v int64) {
			defer wg.Done()
			// Las versiones impares suspenden y las pares reactivan.
			if _, err := repo.ApplySuspension(ctx, user.UserID, v%2 == 1, now, v); err != nil {
				t.Errorf("ApplySuspension(v%d) error = %v", v, err)
			}
		}(version)
	}
	wg.Wait()

	suspendedAt, version := suspensionState(t, db, user.UserID)
	if version != 8 || suspendedAt.Valid {
		t.Errorf("state = suspended_at valid %v, version %d; want active at version 8", suspendedAt.Valid, version)
	}
}
