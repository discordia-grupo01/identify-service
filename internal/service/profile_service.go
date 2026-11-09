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

	_ "golang.org/x/image/webp"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/profileimage"
)

const (
	MaxProfileImageBytes = 5 * 1024 * 1024
	MaxProfileImageSide  = 4096
	MaxDescriptionRunes  = 500
	MaxNameRunes         = 100
	MaxStatusTextRunes   = 64
	MaxStatusEmojiBytes  = 32
)

var (
	ErrInvalidProfileName        = errors.New("el nombre contiene caracteres inválidos o supera los 100 caracteres")
	ErrInvalidProfileDescription = errors.New("la descripción contiene caracteres inválidos o supera los 500 caracteres")
	ErrInvalidProfileImage       = errors.New("la imagen debe ser JPEG, PNG, WebP o GIF, pesar hasta 5 MiB y tener dimensiones válidas")
	ErrUserNotFound              = errors.New("el usuario no existe")
	ErrNoStatusChanges           = errors.New("no hay cambios para guardar")
	ErrInvalidStatusText         = errors.New("el texto del estado contiene caracteres de control o supera los 64 caracteres")
	ErrInvalidStatusEmoji        = errors.New("el emoji del estado contiene caracteres de control o supera los 32 bytes")
)

type ProfileService interface {
	Update(ctx context.Context, userID string, request model.ProfileUpdateRequest) (*model.UserResponse, error)
	GetProfile(ctx context.Context, userID string) (*model.UserResponse, error)
	GetPublicProfile(ctx context.Context, viewerUserID string, targetUserID string) (*model.PublicUserResponse, error)
	UpdateStatus(ctx context.Context, userID string, request model.StatusUpdateRequest) (*model.UserResponse, error)
	ClearStatus(ctx context.Context, userID string) error
}

type profileService struct {
	repo           repository.UserRepository
	storage        profileimage.Storage
	membershipRepo repository.ServerMembershipRepository
}

func NewProfileService(repo repository.UserRepository, storage profileimage.Storage, membershipRepo repository.ServerMembershipRepository) ProfileService {
	return &profileService{repo: repo, storage: storage, membershipRepo: membershipRepo}
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

func (s *profileService) GetPublicProfile(ctx context.Context, viewerUserID string, targetUserID string) (*model.PublicUserResponse, error) {
	user, err := s.repo.GetByID(ctx, targetUserID)
	if err != nil {
		return nil, err
	}
	// Un suspendido desaparece del perfil público: se responde igual que si
	// no existiera, para no revelar el estado de la cuenta a terceros.
	if user == nil || user.SuspendedAt != nil {
		return nil, ErrUserNotFound
	}
	response := model.NewPublicUserResponse(user)

	// Compararse con uno mismo no tiene sentido; se evita además una consulta
	// innecesaria al repositorio de membresías.
	if viewerUserID != targetUserID {
		mutualServerIDs, err := s.membershipRepo.ListMutualServerIDs(ctx, viewerUserID, targetUserID)
		if err != nil {
			return nil, err
		}
		response.MutualServerIDs = mutualServerIDs
	}
	return response, nil
}

func (s *profileService) UpdateStatus(ctx context.Context, userID string, request model.StatusUpdateRequest) (*model.UserResponse, error) {
	if request.StatusText == nil && request.StatusEmoji == nil {
		return nil, ErrNoStatusChanges
	}

	var statusText, statusEmoji *string
	if request.StatusText != nil {
		normalized, err := normalizeStatusText(*request.StatusText)
		if err != nil {
			return nil, err
		}
		statusText = &normalized
	}
	if request.StatusEmoji != nil {
		normalized, err := normalizeStatusEmoji(*request.StatusEmoji)
		if err != nil {
			return nil, err
		}
		statusEmoji = &normalized
	}

	// Merge atómico en la base (UPDATE ... COALESCE ... RETURNING): un campo
	// no enviado (nil acá) no pisa el valor actual, sin necesidad de leerlo
	// primero. Evita el race condition de leer-modificar-escribir en
	// aplicación cuando dos requests tocan campos distintos en paralelo.
	user, err := s.repo.UpdateStatus(ctx, userID, statusText, statusEmoji)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	return model.NewUserResponse(user), nil
}

func (s *profileService) ClearStatus(ctx context.Context, userID string) error {
	empty := ""
	user, err := s.repo.UpdateStatus(ctx, userID, &empty, &empty)
	if err != nil {
		return err
	}
	if user == nil {
		return ErrUserNotFound
	}
	return nil
}

func (s *profileService) Update(ctx context.Context, userID string, request model.ProfileUpdateRequest) (*model.UserResponse, error) {
	user, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	if request.Name == nil && request.Description == nil && !request.ImageProvided && !request.RemoveImage {
		return nil, errors.New("no hay cambios para guardar")
	}
	if request.ImageProvided && request.RemoveImage {
		return nil, ErrInvalidProfileImage
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
	} else if request.RemoveImage {
		avatarURL = ""
	}

	sameObject := profileimage.SameObject(avatarURL, oldAvatarURL)
	if err := s.repo.UpdateProfile(ctx, userID, name, description, avatarURL); err != nil {
		if !sameObject && avatarURL != "" {
			_ = s.storage.Delete(ctx, avatarURL)
		}
		return nil, err
	}
	if !sameObject && oldAvatarURL != "" {
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

func normalizeStatusText(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > MaxStatusTextRunes {
		return "", ErrInvalidStatusText
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", ErrInvalidStatusText
		}
	}
	return value, nil
}

func normalizeStatusEmoji(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || len(value) > MaxStatusEmojiBytes {
		return "", ErrInvalidStatusEmoji
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", ErrInvalidStatusEmoji
		}
	}
	return value, nil
}

func validateProfileImage(data []byte) (string, error) {
	if len(data) == 0 || len(data) > MaxProfileImageBytes {
		return "", ErrInvalidProfileImage
	}
	contentType := http.DetectContentType(data)
	if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp" && contentType != "image/gif" {
		return "", ErrInvalidProfileImage
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > MaxProfileImageSide || config.Height > MaxProfileImageSide {
		return "", ErrInvalidProfileImage
	}
	return contentType, nil
}
