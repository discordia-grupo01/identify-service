package model

import "time"

type PasswordRecoveryRequest struct {
	Email string `json:"email" binding:"required,email"`
}

type PasswordRecovery struct {
	ID        int64
	UserID    string
	Email     string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}
