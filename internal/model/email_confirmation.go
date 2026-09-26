package model

import "time"

type EmailConfirmation struct {
	ID        int64
	UserID    string
	Email     string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

type EmailConfirmationRequest struct {
	Email string `json:"email" binding:"required,email"`
}
