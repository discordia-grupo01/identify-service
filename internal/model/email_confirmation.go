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

// ReturnToApp pide que el link del mail abra la app mobile en vez de la web.
const ReturnToApp = "app"

type EmailConfirmationRequest struct {
	Email string `json:"email" binding:"required,email"`
	// "app" cuando lo pide la app mobile: el link del mail pasa por una pagina
	// puente de la web que abre la app. Vacio o "web", el link de siempre.
	ReturnTo string `json:"return_to,omitempty" binding:"omitempty,oneof=web app"`
}
