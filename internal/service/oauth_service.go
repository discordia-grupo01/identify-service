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
	ErrOAuthTokenInvalid        = errors.New("el token del proveedor externo es invalido")
	ErrOAuthProviderUnavailable = errors.New("el proveedor de identidad externo no esta disponible")
)

const googleProvider = "google"

type GoogleTokenVerifier interface {
	Verify(ctx context.Context, rawIDToken string) (*googleoauth.Claims, error)
}

type OAuthService interface {
	LoginWithGoogle(ctx context.Context, idToken string) (*model.LoginResult, error)
}

type oauthService struct {
	userRepo     repository.UserRepository
	identityRepo repository.OAuthIdentityRepository
	refreshRepo  repository.RefreshTokenRepository
	verifier     GoogleTokenVerifier
	twoFactor    TwoFactorChallenger
}

// OAuthOption configura dependencias opcionales de oauthService, igual que
// AuthOption en authService: sin TwoFactorChallenger el login federado nunca
// pide segundo factor.
type OAuthOption func(*oauthService)

func WithOAuthTwoFactor(challenger TwoFactorChallenger) OAuthOption {
	return func(s *oauthService) { s.twoFactor = challenger }
}

func NewOAuthService(
	userRepo repository.UserRepository,
	identityRepo repository.OAuthIdentityRepository,
	refreshRepo repository.RefreshTokenRepository,
	verifier GoogleTokenVerifier,
	opts ...OAuthOption,
) OAuthService {
	s := &oauthService{
		userRepo:     userRepo,
		identityRepo: identityRepo,
		refreshRepo:  refreshRepo,
		verifier:     verifier,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// LoginWithGoogle valida el id_token con Google y arma la sesion. Si la
// cuenta a la que se resuelve (nueva o vinculada por email) tiene 2FA activo,
// el login federado tiene que pedirlo igual que el de email/contrasena: sin
// este chequeo alguien con 2FA activo podria esquivarlo por completo con
// "Continuar con Google".
func (s *oauthService) LoginWithGoogle(ctx context.Context, idToken string) (*model.LoginResult, error) {
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

	if s.twoFactor != nil {
		enabled, err := s.twoFactor.IsEnabled(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		if enabled {
			challenge, err := s.twoFactor.StartChallenge(ctx, user.ID)
			if err != nil {
				return nil, err
			}
			return &model.LoginResult{Challenge: challenge}, nil
		}
	}

	auth, err := issueTokenPair(ctx, s.refreshRepo, user, uuid.NewString())
	if err != nil {
		return nil, err
	}
	return &model.LoginResult{Auth: auth}, nil
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
