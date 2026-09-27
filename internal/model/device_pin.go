package model

import "time"

// DevicePin es el PIN de acceso rapido (historia "Registro con PIN"): ata un
// PIN numerico a un dispositivo puntual, no a la cuenta en general. Eso
// permite resolver el login SOLO con el device_id, sin pedir email (CA2), y
// hace que el PIN quede invalido en cualquier otro dispositivo o tras una
// reinstalacion (CA4), porque ese dispositivo nunca tuvo una fila propia
// aca.
type DevicePin struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	DeviceID       string     `json:"device_id"`
	PinHash        string     `json:"-"`
	FailedAttempts int        `json:"-"`
	LockedUntil    *time.Time `json:"-"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type ConfigurePinRequest struct {
	Pin      string `json:"pin" binding:"required"`
	DeviceID string `json:"device_id" binding:"required"`
}

type PinLoginRequest struct {
	Pin      string `json:"pin" binding:"required"`
	DeviceID string `json:"device_id" binding:"required"`
}
