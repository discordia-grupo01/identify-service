package repository

import (
	"context"
	"database/sql"
	"time"
)

// LoginAttemptCounts contains failed login attempts for the three dimensions
// used by the login throttler.
type LoginAttemptCounts struct {
	EmailIP int
	Email   int
	IP      int
}

// LoginAttemptRepository tracks failed login attempts. The combined bucket
// protects normal use, while the email and IP buckets cover attacks spread
// across IPs or accounts.
type LoginAttemptRepository interface {
	CountRecentFailures(ctx context.Context, email string, ip string, pairSince time.Time, emailSince time.Time) (LoginAttemptCounts, error)
	RecordFailure(ctx context.Context, email string, ip string) error
	ClearFailures(ctx context.Context, email string, ip string) error
}

type postgresLoginAttemptRepository struct {
	db *sql.DB
}

func NewLoginAttemptRepository(db *sql.DB) LoginAttemptRepository {
	return &postgresLoginAttemptRepository{db: db}
}

func (r *postgresLoginAttemptRepository) CountRecentFailures(ctx context.Context, email string, ip string, pairSince time.Time, emailSince time.Time) (LoginAttemptCounts, error) {
	var counts LoginAttemptCounts
	err := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE email = $1 AND ip = $2 AND created_at >= $3),
			COUNT(*) FILTER (WHERE email = $1 AND created_at >= $4),
			COUNT(*) FILTER (WHERE ip = $2 AND created_at >= $3)
		FROM login_attempts`, email, ip, pairSince, emailSince).
		Scan(&counts.EmailIP, &counts.Email, &counts.IP)
	return counts, err
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
