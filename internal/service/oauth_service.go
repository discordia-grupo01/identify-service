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
	ErrOAuthTokenInvalid = errors.New("el token del proveedor externo es invalido")
	ErrOAuthProviderUnavailable = errors.New("el proveedor de identidad externo no esta disponible")
)

const googleProvider = "google"

type GoogleTokenVerifier interface {
	Verify(ctx context.Context, rawIDToken string) (*googleoauth.Claims, error)
}

type OAuthService interface {
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
