package repository

import (
	"context"
	"database/sql"
	"time"
)

type ServerMembershipRepository interface {
	// Upsert aplica last-write-wins por occurred_at: si ya existe una fila más
	// nueva para (userID, serverID), el evento se descarta silenciosamente
	// (no es un error, es un evento desordenado que llegó tarde).
	Upsert(ctx context.Context, userID string, serverID string, isMember bool, occurredAt time.Time) error
	// ListMutualServerIDs devuelve los IDs de servidor donde tanto userAID
	// como userBID son miembros activos (is_member = true).
	ListMutualServerIDs(ctx context.Context, userAID string, userBID string) ([]string, error)
}

type postgresServerMembershipRepository struct {
	db *sql.DB
}

func NewServerMembershipRepository(db *sql.DB) ServerMembershipRepository {
	return &postgresServerMembershipRepository{db: db}
}

func (r *postgresServerMembershipRepository) Upsert(ctx context.Context, userID string, serverID string, isMember bool, occurredAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO server_memberships (user_id, server_id, is_member, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, server_id) DO UPDATE
		SET is_member = EXCLUDED.is_member, updated_at = EXCLUDED.updated_at
		WHERE server_memberships.updated_at <= EXCLUDED.updated_at`,
		userID, serverID, isMember, occurredAt)
	return err
}

func (r *postgresServerMembershipRepository) ListMutualServerIDs(ctx context.Context, userAID string, userBID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.server_id
		FROM server_memberships a
		JOIN server_memberships b ON a.server_id = b.server_id
		WHERE a.user_id = $1 AND b.user_id = $2 AND a.is_member AND b.is_member`,
		userAID, userBID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	serverIDs := []string{}
	for rows.Next() {
		var serverID string
		if err := rows.Scan(&serverID); err != nil {
			return nil, err
		}
		serverIDs = append(serverIDs, serverID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return serverIDs, nil
}
