package googleoauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
)

const issuerURL = "https://accounts.google.com"

var (
	// ErrProviderUnavailable indica que no se pudo contactar a Google (red
	// caida, timeout, DNS) ya sea al descubrir su configuracion OIDC o al
	// buscar sus claves publicas para validar la firma del token.
	ErrProviderUnavailable = errors.New("no se pudo contactar al proveedor de identidad externo")
	// ErrInvalidToken indica que Google respondio pero el token en si es
	// invalido: firma incorrecta, expirado, audiencia equivocada o con el
	// email sin verificar.
	ErrInvalidToken = errors.New("el token del proveedor externo es invalido")
)

// Claims son los datos del usuario que nos interesan del ID token de Google,
// ya validado.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
}

// Verifier valida ID tokens de Google contra sus claves publicas (JWKS). La
// inicializacion del discovery document es perezosa: si Google no esta
// disponible al arrancar el servicio no lo tira abajo (mismo criterio que el
// consumer opcional de RabbitMQ en main.go), solo falla con
// ErrProviderUnavailable en el primer intento de verificar un token.
type Verifier struct {
	clientID string

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

func NewVerifier(clientID string) *Verifier {
	return &Verifier{clientID: clientID}
}

func (v *Verifier) Verify(ctx context.Context, rawIDToken string) (*Claims, error) {
	idTokenVerifier, err := v.getVerifier(ctx)
	if err != nil {
		return nil, err
	}

	idToken, err := idTokenVerifier.Verify(ctx, rawIDToken)
	if err != nil {
		if isNetworkError(err) {
			return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !claims.EmailVerified {
		return nil, fmt.Errorf("%w: email no verificado por el proveedor", ErrInvalidToken)
	}

	return &Claims{
		Subject:       idToken.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Name:          claims.Name,
		Picture:       claims.Picture,
	}, nil
}

func (v *Verifier) getVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.verifier != nil {
		return v.verifier, nil
	}

	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	v.verifier = provider.Verifier(&oidc.Config{ClientID: v.clientID})
	return v.verifier, nil
}

func isNetworkError(err error) bool {
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}
