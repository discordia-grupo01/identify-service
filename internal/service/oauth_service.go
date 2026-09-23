package service

import (
	"context"
	"errors"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/googleoauth"

	"github.com/google/uuid"
)

var (
	// ErrOAuthTokenInvalid: Google respondio pero el ID token es invalido
	// (firma, audiencia, expiracion o email sin verificar).
	ErrOAuthTokenInvalid = errors.New("el token del proveedor externo es invalido")
	// ErrOAuthProviderUnavailable: no se pudo contactar a Google (red caida,
	// timeout). El cliente debe ofrecer el login con email y contrasena.
	ErrOAuthProviderUnavailable = errors.New("el proveedor de identidad externo no esta disponible")
)

const googleProvider = "google"

// GoogleTokenVerifier permite reemplazar el verificador real por un stub en
// los tests unitarios, sin depender de red hacia Google.
type GoogleTokenVerifier interface {
	Verify(ctx context.Context, rawIDToken string) (*googleoauth.Claims, error)
}

type OAuthService interface {
	// LoginWithGoogle valida el ID token, vincula la identidad a una cuenta
	// existente por email (CA2) o crea una cuenta nueva (CA1), y devuelve la
	// misma respuesta de sesion que el login por contrasena.
	LoginWithGoogle(ctx context.Context, idToken string) (*model.AuthResponse, error)
}

type oauthService struct {
	userRepo     repository.UserRepository
	identityRepo repository.OAuthIdentityRepository
	refreshRepo  repository.RefreshTokenRepository
	verifier     GoogleTokenVerifier
}

func NewOAuthService(
	userRepo repository.UserRepository,
	identityRepo repository.OAuthIdentityRepository,
	refreshRepo repository.RefreshTokenRepository,
	verifier GoogleTokenVerifier,
) OAuthService {
	return &oauthService{
		userRepo:     userRepo,
		identityRepo: identityRepo,
		refreshRepo:  refreshRepo,
		verifier:     verifier,
	}
}

func (s *oauthService) LoginWithGoogle(ctx context.Context, idToken string) (*model.AuthResponse, error) {
	claims, err := s.verifier.Verify(ctx, idToken)
	if err != nil {
		if errors.Is(err, googleoauth.ErrProviderUnavailable) {
			return nil, ErrOAuthProviderUnavailable
		}
		return nil, ErrOAuthTokenInvalid
	}

	user, err := s.findOrCreateUser(ctx, claims)
	if err != nil {
		return nil, err
	}

	return issueTokenPair(ctx, s.refreshRepo, user, uuid.NewString())
}

func (s *oauthService) findOrCreateUser(ctx context.Context, claims *googleoauth.Claims) (*model.User, error) {
	identity, err := s.identityRepo.GetByProvider(ctx, googleProvider, claims.Subject)
	if err != nil {
		return nil, err
	}
	if identity != nil {
		user, err := s.userRepo.GetByID(ctx, identity.UserID)
		if err != nil {
			return nil, err
		}
		if user == nil {
			return nil, ErrOAuthTokenInvalid
		}
		return user, nil
	}

	user, err := s.userRepo.GetByEmail(ctx, claims.Email)
	if err != nil {
		return nil, err
	}
	if user == nil {
		// CA1: no existe cuenta con ese email, se crea una nueva asociada al
		// proveedor externo. Sin password: solo puede iniciar sesion via OAuth.
		user = &model.User{
			ID:            uuid.NewString(),
			Name:          claims.Name,
			Email:         claims.Email,
			AvatarURL:     claims.Picture,
			EmailVerified: true,
		}
		if err := s.userRepo.Create(ctx, user); err != nil {
			return nil, err
		}
	}

	// CA2 (o recien creado en CA1): vincula la identidad federada a la cuenta.
	if err := s.identityRepo.Create(ctx, &model.OAuthIdentity{
		ID:              uuid.NewString(),
		UserID:          user.ID,
		Provider:        googleProvider,
		ProviderSubject: claims.Subject,
		Email:           claims.Email,
	}); err != nil {
		return nil, err
	}

	return user, nil
}
