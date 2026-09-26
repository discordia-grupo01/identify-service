package model

import "time"

// UserTwoFactor es el segundo factor de una cuenta. Nace pendiente (con
// ConfirmedAt en nil) cuando el usuario pide el QR, y recien queda activo
// cuando confirma un codigo generado por su app. Mientras este
// pendiente, el login sigue funcionando como siempre: un QR escaneado a
// medias no puede dejar a nadie afuera de su cuenta.
type UserTwoFactor struct {
	UserID string
	// Secret viaja cifrado hacia y desde la BDD. En memoria siempre es el
	// base32 en claro que espera la app autenticadora.
	Secret       string
	ConfirmedAt  *time.Time
	LastUsedStep int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IsActive distingue "activo" de "pendiente de confirmacion".
func (tf *UserTwoFactor) IsActive() bool {
	return tf != nil && tf.ConfirmedAt != nil
}

// TwoFactorChallenge es el login a medio hacer: la contraseña ya se verifico
// pero todavia no hay sesion. No es una credencial (no sirve para llamar a
// ningun endpoint autenticado), solo identifica cual login se esta
// completando en el segundo paso.
type TwoFactorChallenge struct {
	ID        string
	UserID    string
	TokenHash string
	Attempts  int
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// TwoFactorActivateRequest confirma el QR recien escaneado (CA1).
type TwoFactorActivateRequest struct {
	Code string `json:"code" binding:"required"`
}

// TwoFactorVerifyRequest completa el login. Code acepta tanto el codigo de 6
// digitos de la app (CA2) como un codigo de recuperacion (CA4): son el mismo
// paso para el usuario, asi que son el mismo endpoint.
type TwoFactorVerifyRequest struct {
	ChallengeToken string `json:"challenge_token" binding:"required"`
	Code           string `json:"code" binding:"required"`
}

// TwoFactorPasswordRequest cubre las dos operaciones sensibles sobre un 2FA ya
// activo: desactivarlo (CA5) y regenerar la lista de codigos (CA4). Las dos
// reingresan la contraseña, porque las dos le devuelven el acceso a quien
// tenga la sesion abierta.
type TwoFactorPasswordRequest struct {
	Password string `json:"password" binding:"required"`
}

// TwoFactorSetupResponse es lo que necesita la pantalla del QR. Devuelve la
// URI otpauth:// y no una imagen: el QR lo dibuja cada app, que ya sabe
// renderizar en su propia plataforma. Secret va aparte para el enlace de
// "no puedo escanear, ingresar el codigo a mano".
type TwoFactorSetupResponse struct {
	Secret     string `json:"secret"`
	OtpauthURL string `json:"otpauth_url"`
	Issuer     string `json:"issuer"`
	Account    string `json:"account"`
}

// TwoFactorStatusResponse alimenta la pantalla de seguridad de la cuenta.
type TwoFactorStatusResponse struct {
	Enabled                bool `json:"enabled"`
	PendingActivation      bool `json:"pending_activation"`
	RecoveryCodesRemaining int  `json:"recovery_codes_remaining"`
}

// TwoFactorRecoveryCodesResponse es la unica vez que los codigos viajan en
// claro: despues solo queda su hash.
type TwoFactorRecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// TwoFactorChallengeResponse es la respuesta del login cuando la cuenta tiene
// 2FA activo. No trae user ni token: todavia no hay sesion (CA2).
type TwoFactorChallengeResponse struct {
	TwoFactorRequired bool   `json:"two_factor_required"`
	ChallengeToken    string `json:"challenge_token"`
	ExpiresIn         int    `json:"expires_in"`
}

// LoginResult es lo que devuelve el login: o una sesion, o un desafio de
// segundo factor. Es un tipo aparte y no dos campos opcionales dentro de
// AuthResponse para que el resto de las formas de entrar (OAuth, PIN, refresh)
// sigan devolviendo exactamente el mismo JSON que antes.
type LoginResult struct {
	Auth      *AuthResponse
	Challenge *TwoFactorChallengeResponse
}

// TwoFactorVerifyResponse es la sesion ya otorgada, mas el aviso de CA4:
// cuando se entro con un codigo de recuperacion hay que recomendar regenerar
// la lista, y para eso el front necesita saber que se uso uno y cuantos
// quedan.
type TwoFactorVerifyResponse struct {
	AuthResponse
	RecoveryCodeUsed       bool `json:"recovery_code_used"`
	RecoveryCodesRemaining int  `json:"recovery_codes_remaining,omitempty"`
}
