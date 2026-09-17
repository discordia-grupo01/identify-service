package model

import "time"

// OAuthIdentity vincula una cuenta (User) con la identidad que un proveedor
// externo (ej. Google) le asigna. Un mismo User puede tener varias, una por
// proveedor con el que se vinculo.
type OAuthIdentity struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	Provider        string    `json:"provider"`
	ProviderSubject string    `json:"provider_subject"`
	Email           string    `json:"email"`
	CreatedAt       time.Time `json:"created_at"`
}
