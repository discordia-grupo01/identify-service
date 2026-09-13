package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/profileimage"
)

const (
	MaxProfileImageBytes = 5 * 1024 * 1024
	MaxProfileImageSide  = 4096
	MaxDescriptionRunes  = 500
	MaxNameRunes         = 100
)

var (
	ErrInvalidProfileName        = errors.New("el nombre contiene caracteres inválidos o supera los 100 caracteres")
	ErrInvalidProfileDescription = errors.New("la descripción contiene caracteres inválidos o supera los 500 caracteres")
	ErrInvalidProfileImage       = errors.New("la imagen debe ser JPEG, PNG o GIF, pesar hasta 5 MiB y tener dimensiones válidas")
	ErrUserNotFound              = errors.New("el usuario no existe")
)

type ProfileService interface {
	Update(ctx context.Context, userID string, request model.ProfileUpdateRequest) (*model.UserResponse, error)
	GetProfile(ctx context.Context, userID string) (*model.UserResponse, error)
	GetPublicProfile(ctx context.Context, targetUserID string) (*model.PublicUserResponse, error)
}

type profileService struct {
	repo    repository.UserRepository
	storage profileimage.Storage
}

func NewProfileService(repo repository.UserRepository, storage profileimage.Storage) ProfileService {
	return &profileService{repo: repo, storage: storage}
}

func (s *profileService) GetProfile(ctx context.Context, userID string) (*model.UserResponse, error) {
	user, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	return model.NewUserResponse(user), nil
}

func (s *profileService) GetPublicProfile(ctx context.Context, targetUserID string) (*model.PublicUserResponse, error) {
	user, err := s.repo.GetByID(ctx, targetUserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	return model.NewPublicUserResponse(user), nil
}

func (s *profileService) Update(ctx context.Context, userID string, request model.ProfileUpdateRequest) (*model.UserResponse, error) {
	user, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	if request.Name == nil && request.Description == nil && !request.ImageProvided {
		return nil, errors.New("no hay cambios para guardar")
	}

	name := user.Name
	if request.Name != nil {
		name, err = normalizeProfileName(*request.Name)
		if err != nil {
			return nil, err
		}
	}
	description := user.Description
	if request.Description != nil {
		description, err = normalizeDescription(*request.Description)
		if err != nil {
			return nil, err
		}
	}

	avatarURL := user.AvatarURL
	oldAvatarURL := user.AvatarURL
	if request.ImageProvided {
		contentType, err := validateProfileImage(request.Image)
		if err != nil || contentType != request.ImageContentType {
			return nil, ErrInvalidProfileImage
		}
		avatarURL, err = s.storage.Save(ctx, userID, request.Image, contentType)
		if err != nil {
			return nil, err
		}
	}

	if err := s.repo.UpdateProfile(ctx, userID, name, description, avatarURL); err != nil {
		if avatarURL != oldAvatarURL && avatarURL != "" {
			_ = s.storage.Delete(ctx, avatarURL)
		}
		return nil, err
	}
	if avatarURL != oldAvatarURL && oldAvatarURL != "" {
		_ = s.storage.Delete(ctx, oldAvatarURL)
	}

	user.Name = name
	user.Description = description
	user.AvatarURL = avatarURL
	return model.NewUserResponse(user), nil
}

func normalizeProfileName(value string) (string, error) {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" || utf8.RuneCountInString(value) > MaxNameRunes || !utf8.ValidString(value) {
		return "", ErrInvalidProfileName
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsMark(character) || unicode.IsDigit(character) {
			continue
		}
		if strings.ContainsRune(" '-.", character) {
			continue
		}
		return "", ErrInvalidProfileName
	}
	return value, nil
}

func normalizeDescription(value string) (string, error) {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > MaxDescriptionRunes {
		return "", ErrInvalidProfileDescription
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t' {
			return "", ErrInvalidProfileDescription
		}
	}
	return strings.TrimSpace(value), nil
}

func validateProfileImage(data []byte) (string, error) {
	if len(data) == 0 || len(data) > MaxProfileImageBytes {
		return "", ErrInvalidProfileImage
	}
	contentType := http.DetectContentType(data)
	if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/gif" {
		return "", ErrInvalidProfileImage
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > MaxProfileImageSide || config.Height > MaxProfileImageSide {
		return "", ErrInvalidProfileImage
	}
	return contentType, nil
}
