package model

import "time"

const AccountRecoveryMessageMaxLength = 500

type AccountRecoveryStatus string

const (
	AccountRecoveryPending  AccountRecoveryStatus = "pending"
	AccountRecoveryApproved AccountRecoveryStatus = "approved"
	AccountRecoveryRejected AccountRecoveryStatus = "rejected"
)

type AccountRecoveryRequest struct {
	ID         string
	UserID     string
	Message    string
	Status     AccountRecoveryStatus
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

type AccountRecoveryCreateRequest struct {
	ChallengeToken string `json:"challenge_token" binding:"required"`
	Message        string `json:"message"`
}

type AccountRecoveryCreateResponse struct {
	RequestID string                `json:"request_id"`
	Status    AccountRecoveryStatus `json:"status"`
	EmailHint string                `json:"email_hint"`
}
