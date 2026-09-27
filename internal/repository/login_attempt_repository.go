package repository

import (
	"context"
	"database/sql"
	"time"
)

// LoginAttemptRepository tracks failed login attempts per (email, ip) pair,
// so brute-forcing one account only throttles the attacker's own IP instead
// of locking the real account owner out.
type LoginAttemptRepository interface {
	CountRecentFailures(ctx context.Context, email string, ip string, since time.Time) (int, error)
	RecordFailure(ctx context.Context, email string, ip string) error
	ClearFailures(ctx context.Context, email string, ip string) error
}

type postgresLoginAttemptRepository struct {
	db *sql.DB
}

func NewLoginAttemptRepository(db *sql.DB) LoginAttemptRepository {
	return &postgresLoginAttemptRepository{db: db}
}

func (r *postgresLoginAttemptRepository) CountRecentFailures(ctx context.Context, email string, ip string, since time.Time) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM login_attempts
		WHERE email = $1 AND ip = $2 AND created_at >= $3`, email, ip, since).Scan(&count)
	return count, err
}

func (r *postgresLoginAttemptRepository) RecordFailure(ctx context.Context, email string, ip string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO login_attempts (email, ip) VALUES ($1, $2)`, email, ip)
	return err
}

func (r *postgresLoginAttemptRepository) ClearFailures(ctx context.Context, email string, ip string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM login_attempts WHERE email = $1 AND ip = $2`, email, ip)
	return err
}
