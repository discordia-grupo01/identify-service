package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

type profileImageStorageStub struct {
	url       string
	data      []byte
	deleteErr error
	saveErr   error
	deleted   string
}

func (s *profileImageStorageStub) Save(_ context.Context, _ string, data []byte, _ string) (string, error) {
	if s.saveErr != nil {
		return "", s.saveErr
	}
	s.data = data
	return s.url, nil
}

func (s *profileImageStorageStub) Delete(_ context.Context, imageURL string) error {
	s.deleted = imageURL
	return s.deleteErr
}

type serverMembershipRepositoryStub struct {
	mutualServerIDs []string
	err             error
	called          bool
	calledUserAID   string
	calledUserBID   string
}

func (s *serverMembershipRepositoryStub) Upsert(_ context.Context, _ string, _ string, _ bool, _ time.Time) error {
	return nil
}

func (s *serverMembershipRepositoryStub) ListMutualServerIDs(_ context.Context, userAID string, userBID string) ([]string, error) {
	s.called = true
	s.calledUserAID = userAID
	s.calledUserBID = userBID
	return s.mutualServerIDs, s.err
}

func TestProfileServiceUpdatesProfileAndAvatar(t *testing.T) {
	user := &model.User{ID: "user-id", Name: "Ada Lovelace", Description: "Old description", AvatarURL: "/old.png"}
	repo := &userRepositoryStub{existingUser: user}
	storage := &profileImageStorageStub{url: "/uploads/profile-images/user-id.png"}
	profile := NewProfileService(repo, storage, &serverMembershipRepositoryStub{})
	name := "Áda O'Connor"
	description := "Updated profile"

	response, err := profile.Update(context.Background(), "user-id", model.ProfileUpdateRequest{
		Name: &name, Description: &description, Image: validPNG(t), ImageContentType: "image/png", ImageProvided: true,
	})

	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if response.Name != name || response.Description != description || response.AvatarURL != storage.url {
		t.Fatalf("unexpected response: %+v", response)
	}
	if user.Name != name || user.Description != description || user.AvatarURL != storage.url {
		t.Fatalf("profile was not persisted: %+v", user)
	}
	if storage.deleted != "/old.png" {
		t.Fatalf("old avatar = %q, want it deleted", storage.deleted)
	}
}

func TestProfileServiceRejectsInvalidProfileData(t *testing.T) {
	tests := []struct {
		name    string
		request model.ProfileUpdateRequest
		want    error
	}{
		{name: "invalid name symbol", request: model.ProfileUpdateRequest{Name: stringPointer("Ada <admin>")}, want: ErrInvalidProfileName},
		{name: "control character in name", request: model.ProfileUpdateRequest{Name: stringPointer("Ada\x00")}, want: ErrInvalidProfileName},
		{name: "description control character", request: model.ProfileUpdateRequest{Description: stringPointer("hello\x00")}, want: ErrInvalidProfileDescription},
		{name: "fake image", request: model.ProfileUpdateRequest{Image: []byte("not an image"), ImageContentType: "image/png", ImageProvided: true}, want: ErrInvalidProfileImage},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada"}}
			_, err := NewProfileService(repo, &profileImageStorageStub{url: "/new.png"}, &serverMembershipRepositoryStub{}).Update(context.Background(), "user-id", test.request)
			if !errors.Is(err, test.want) {
				t.Fatalf("Update() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestProfileServiceNormalizesNameAndDescription(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Old"}}
	profile := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{})
	name := "  Ada   Lovelace  "
	description := "  A profile\n  description  "

	response, err := profile.Update(context.Background(), "user-id", model.ProfileUpdateRequest{Name: &name, Description: &description})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if response.Name != "Ada Lovelace" || response.Description != "A profile\n  description" {
		t.Fatalf("normalized response = %+v", response)
	}
}

func TestProfileServiceRejectsNoChanges(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada"}}
	_, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).Update(context.Background(), "user-id", model.ProfileUpdateRequest{})
	if err == nil || err.Error() != "no hay cambios para guardar" {
		t.Fatalf("Update() error = %v, want no changes error", err)
	}
}

func TestProfileServicePropagatesRepositoryAndStorageErrors(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	storageErr := errors.New("storage unavailable")

	t.Run("get user", func(t *testing.T) {
		repo := &userRepositoryStub{getErr: databaseErr}
		_, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).Update(context.Background(), "user-id", model.ProfileUpdateRequest{Name: stringPointer("Ada")})
		if !errors.Is(err, databaseErr) {
			t.Fatalf("Update() error = %v, want %v", err, databaseErr)
		}
	})

	t.Run("save image", func(t *testing.T) {
		repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada"}}
		storage := &profileImageStorageStub{saveErr: storageErr}
		_, err := NewProfileService(repo, storage, &serverMembershipRepositoryStub{}).Update(context.Background(), "user-id", model.ProfileUpdateRequest{Image: validPNG(t), ImageContentType: "image/png", ImageProvided: true})
		if !errors.Is(err, storageErr) {
			t.Fatalf("Update() error = %v, want %v", err, storageErr)
		}
	})

	t.Run("update profile", func(t *testing.T) {
		repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada", AvatarURL: "/old.png"}, updateErr: databaseErr}
		storage := &profileImageStorageStub{url: "/new.png"}
		_, err := NewProfileService(repo, storage, &serverMembershipRepositoryStub{}).Update(context.Background(), "user-id", model.ProfileUpdateRequest{Image: validPNG(t), ImageContentType: "image/png", ImageProvided: true})
		if !errors.Is(err, databaseErr) {
			t.Fatalf("Update() error = %v, want %v", err, databaseErr)
		}
		if storage.deleted != "/new.png" {
			t.Fatalf("new avatar = %q, want cleanup after persistence failure", storage.deleted)
		}
	})
}

func TestProfileServiceRejectsMismatchedImageContentType(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada"}}
	storage := &profileImageStorageStub{}
	_, err := NewProfileService(repo, storage, &serverMembershipRepositoryStub{}).Update(context.Background(), "user-id", model.ProfileUpdateRequest{
		Image: validPNG(t), ImageContentType: "image/jpeg", ImageProvided: true,
	})
	if !errors.Is(err, ErrInvalidProfileImage) {
		t.Fatalf("Update() error = %v, want %v", err, ErrInvalidProfileImage)
	}
}

func TestProfileServiceRejectsOversizedImageDimensions(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada"}}

	largeImage := image.NewRGBA(image.Rect(0, 0, 4097, 1))
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, largeImage); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}

	req := model.ProfileUpdateRequest{
		Image:            buffer.Bytes(),
		ImageContentType: "image/png",
		ImageProvided:    true,
	}

	_, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).Update(context.Background(), "user-id", req)
	if !errors.Is(err, ErrInvalidProfileImage) {
		t.Fatalf("Update() error = %v, want %v", err, ErrInvalidProfileImage)
	}
}

func TestProfileServicePartiallyUpdatesProfile(t *testing.T) {
	user := &model.User{
		ID:          "user-id",
		Name:        "Ada Lovelace",
		Description: "Original description",
		AvatarURL:   "/avatar.png",
	}
	repo := &userRepositoryStub{existingUser: user}
	profile := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{})

	newName := "Ada Byron"
	response, err := profile.Update(context.Background(), "user-id", model.ProfileUpdateRequest{
		Name: &newName,
	})

	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if response.Name != "Ada Byron" {
		t.Fatalf("Name = %q, want Ada Byron", response.Name)
	}
	if response.Description != "Original description" {
		t.Fatalf("Description = %q, want Original description (preserved)", response.Description)
	}
}

func TestProfileServiceDeletesOldAvatarOnUpdate(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada", AvatarURL: "/old.png"}}
	storage := &profileImageStorageStub{url: "/new.png"}
	_, err := NewProfileService(repo, storage, &serverMembershipRepositoryStub{}).Update(context.Background(), "user-id", model.ProfileUpdateRequest{Image: validPNG(t), ImageContentType: "image/png", ImageProvided: true})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if storage.deleted != "/old.png" {
		t.Fatalf("old avatar = %q, want it deleted", storage.deleted)
	}
}

func TestProfileServiceDoesNotDeleteOldAvatarIfNotChanged(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada", AvatarURL: "/avatar.png"}}
	storage := &profileImageStorageStub{url: "/avatar.png"}
	_, err := NewProfileService(repo, storage, &serverMembershipRepositoryStub{}).Update(context.Background(), "user-id", model.ProfileUpdateRequest{Name: stringPointer("Ada Lovelace")})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if storage.deleted != "" {
		t.Fatalf("avatar = %q, want no deletion", storage.deleted)
	}
}

func TestProfileServiceReturnsNotFoundForNonexistentUser(t *testing.T) {
	repo := &userRepositoryStub{existingUser: nil}
	_, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).Update(context.Background(), "user-id", model.ProfileUpdateRequest{Name: stringPointer("Ada")})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("Update() error = %v, want %v", err, ErrUserNotFound)
	}
}

func TestProfileServiceReturnsNotFoundForGetProfile(t *testing.T) {
	repo := &userRepositoryStub{existingUser: nil}
	_, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).GetProfile(context.Background(), "user-id")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("GetProfile() error = %v, want %v", err, ErrUserNotFound)
	}
}

func TestProfileServiceReturnsPublicProfile(t *testing.T) {
	createdAt := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	repo := &userRepositoryStub{existingUser: &model.User{
		ID: "user-id", Name: "Ada", Email: "ada@example.com", Description: "A profile",
		AvatarURL: "/avatar.png", CreatedAt: createdAt,
	}}
	response, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).GetPublicProfile(context.Background(), "viewer-id", "user-id")
	if err != nil {
		t.Fatalf("GetPublicProfile() error = %v", err)
	}
	if response.ID != "user-id" || response.Name != "Ada" || response.Description != "A profile" || response.AvatarURL != "/avatar.png" || !response.CreatedAt.Equal(createdAt) {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestProfileServiceReturnsNotFoundForPublicProfile(t *testing.T) {
	repo := &userRepositoryStub{existingUser: nil}
	_, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).GetPublicProfile(context.Background(), "viewer-id", "user-id")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("GetPublicProfile() error = %v, want %v", err, ErrUserNotFound)
	}
}

func TestProfileServicePropagatesRepositoryErrorForPublicProfile(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	repo := &userRepositoryStub{getErr: databaseErr}
	_, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).GetPublicProfile(context.Background(), "viewer-id", "user-id")
	if !errors.Is(err, databaseErr) {
		t.Fatalf("GetPublicProfile() error = %v, want %v", err, databaseErr)
	}
}

func TestProfileServiceReturnsMutualServerIDs(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "target-id", Name: "Ada"}}
	membershipRepo := &serverMembershipRepositoryStub{mutualServerIDs: []string{"server-1", "server-2"}}

	response, err := NewProfileService(repo, &profileImageStorageStub{}, membershipRepo).GetPublicProfile(context.Background(), "viewer-id", "target-id")
	if err != nil {
		t.Fatalf("GetPublicProfile() error = %v", err)
	}
	if membershipRepo.calledUserAID != "viewer-id" || membershipRepo.calledUserBID != "target-id" {
		t.Fatalf("ListMutualServerIDs called with (%q, %q), want (viewer-id, target-id)", membershipRepo.calledUserAID, membershipRepo.calledUserBID)
	}
	if len(response.MutualServerIDs) != 2 || response.MutualServerIDs[0] != "server-1" || response.MutualServerIDs[1] != "server-2" {
		t.Fatalf("MutualServerIDs = %v, want [server-1 server-2]", response.MutualServerIDs)
	}
}

func TestProfileServiceReturnsEmptyMutualServerIDsWhenNoneShared(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "target-id", Name: "Ada"}}
	membershipRepo := &serverMembershipRepositoryStub{mutualServerIDs: []string{}}

	response, err := NewProfileService(repo, &profileImageStorageStub{}, membershipRepo).GetPublicProfile(context.Background(), "viewer-id", "target-id")
	if err != nil {
		t.Fatalf("GetPublicProfile() error = %v", err)
	}
	if response.MutualServerIDs == nil || len(response.MutualServerIDs) != 0 {
		t.Fatalf("MutualServerIDs = %v, want empty non-nil slice", response.MutualServerIDs)
	}
}

func TestProfileServiceReturnsEmptyMutualServerIDsWhenViewerIsTarget(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada"}}
	membershipRepo := &serverMembershipRepositoryStub{mutualServerIDs: []string{"server-1"}}

	response, err := NewProfileService(repo, &profileImageStorageStub{}, membershipRepo).GetPublicProfile(context.Background(), "user-id", "user-id")
	if err != nil {
		t.Fatalf("GetPublicProfile() error = %v", err)
	}
	if membershipRepo.called {
		t.Fatal("ListMutualServerIDs no debería llamarse cuando viewer == target")
	}
	if response.MutualServerIDs == nil || len(response.MutualServerIDs) != 0 {
		t.Fatalf("MutualServerIDs = %v, want [] cuando viewer == target", response.MutualServerIDs)
	}
}

func TestProfileServicePropagatesMembershipRepositoryError(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	repo := &userRepositoryStub{existingUser: &model.User{ID: "target-id", Name: "Ada"}}
	membershipRepo := &serverMembershipRepositoryStub{err: databaseErr}

	_, err := NewProfileService(repo, &profileImageStorageStub{}, membershipRepo).GetPublicProfile(context.Background(), "viewer-id", "target-id")
	if !errors.Is(err, databaseErr) {
		t.Fatalf("GetPublicProfile() error = %v, want %v", err, databaseErr)
	}
}

func TestProfileServiceUpdateStatusSetsBothFields(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada"}}
	text := "Jugando"
	emoji := "🎮"

	response, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).UpdateStatus(context.Background(), "user-id", model.StatusUpdateRequest{
		StatusText: &text, StatusEmoji: &emoji,
	})
	if err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	if response.StatusText != "Jugando" || response.StatusEmoji != "🎮" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if repo.existingUser.StatusText != "Jugando" || repo.existingUser.StatusEmoji != "🎮" {
		t.Fatalf("status was not persisted: %+v", repo.existingUser)
	}
}

func TestProfileServiceUpdateStatusKeepsUntouchedField(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", StatusText: "Old text", StatusEmoji: "🎮"}}
	newText := "New text"

	response, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).UpdateStatus(context.Background(), "user-id", model.StatusUpdateRequest{
		StatusText: &newText,
	})
	if err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	if response.StatusText != "New text" || response.StatusEmoji != "🎮" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestProfileServiceUpdateStatusClearsFieldWithEmptyString(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", StatusText: "Old text", StatusEmoji: "🎮"}}
	empty := ""

	response, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).UpdateStatus(context.Background(), "user-id", model.StatusUpdateRequest{
		StatusText: &empty,
	})
	if err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	if response.StatusText != "" || response.StatusEmoji != "🎮" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestProfileServiceUpdateStatusClearsEmojiWithEmptyStringKeepingText(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", StatusText: "Old text", StatusEmoji: "🎮"}}
	empty := ""

	response, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).UpdateStatus(context.Background(), "user-id", model.StatusUpdateRequest{
		StatusEmoji: &empty,
	})
	if err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	if response.StatusText != "Old text" || response.StatusEmoji != "" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestProfileServiceUpdateStatusRejectsNoChanges(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id"}}
	_, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).UpdateStatus(context.Background(), "user-id", model.StatusUpdateRequest{})
	if !errors.Is(err, ErrNoStatusChanges) {
		t.Fatalf("UpdateStatus() error = %v, want %v", err, ErrNoStatusChanges)
	}
}

func TestProfileServiceUpdateStatusRejectsInvalidData(t *testing.T) {
	longTextValue := ""
	for i := 0; i < MaxStatusTextRunes+1; i++ {
		longTextValue += "a"
	}
	longEmojiValue := ""
	for i := 0; i < MaxStatusEmojiBytes+1; i++ {
		longEmojiValue += "a"
	}
	controlText := "hello\x00"
	controlEmoji := "\x00"

	tests := []struct {
		name    string
		request model.StatusUpdateRequest
		want    error
	}{
		{name: "text too long", request: model.StatusUpdateRequest{StatusText: &longTextValue}, want: ErrInvalidStatusText},
		{name: "text control character", request: model.StatusUpdateRequest{StatusText: &controlText}, want: ErrInvalidStatusText},
		{name: "emoji too long", request: model.StatusUpdateRequest{StatusEmoji: &longEmojiValue}, want: ErrInvalidStatusEmoji},
		{name: "emoji control character", request: model.StatusUpdateRequest{StatusEmoji: &controlEmoji}, want: ErrInvalidStatusEmoji},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id"}}
			_, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).UpdateStatus(context.Background(), "user-id", test.request)
			if !errors.Is(err, test.want) {
				t.Fatalf("UpdateStatus() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestProfileServiceUpdateStatusReturnsNotFoundForNonexistentUser(t *testing.T) {
	repo := &userRepositoryStub{existingUser: nil}
	text := "Jugando"
	_, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).UpdateStatus(context.Background(), "user-id", model.StatusUpdateRequest{StatusText: &text})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("UpdateStatus() error = %v, want %v", err, ErrUserNotFound)
	}
}

func TestProfileServiceUpdateStatusPropagatesRepositoryError(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id"}, updateErr: databaseErr}
	text := "Jugando"
	_, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).UpdateStatus(context.Background(), "user-id", model.StatusUpdateRequest{StatusText: &text})
	if !errors.Is(err, databaseErr) {
		t.Fatalf("UpdateStatus() error = %v, want %v", err, databaseErr)
	}
}

func TestProfileServiceClearStatusClearsBothFields(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", StatusText: "Old text", StatusEmoji: "🎮"}}
	if err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).ClearStatus(context.Background(), "user-id"); err != nil {
		t.Fatalf("ClearStatus() error = %v", err)
	}
	if repo.existingUser.StatusText != "" || repo.existingUser.StatusEmoji != "" {
		t.Fatalf("status was not cleared: %+v", repo.existingUser)
	}
}

func TestProfileServiceClearStatusIsIdempotent(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id"}}
	profile := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{})
	if err := profile.ClearStatus(context.Background(), "user-id"); err != nil {
		t.Fatalf("ClearStatus() error = %v", err)
	}
	if err := profile.ClearStatus(context.Background(), "user-id"); err != nil {
		t.Fatalf("ClearStatus() second call error = %v", err)
	}
}

func TestProfileServiceClearStatusReturnsNotFoundForNonexistentUser(t *testing.T) {
	repo := &userRepositoryStub{existingUser: nil}
	err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).ClearStatus(context.Background(), "user-id")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("ClearStatus() error = %v, want %v", err, ErrUserNotFound)
	}
}

func TestProfileServiceReturnsProfile(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada", Description: "A profile", AvatarURL: "/avatar.png"}}
	response, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).GetProfile(context.Background(), "user-id")
	if err != nil {
		t.Fatalf("GetProfile() error = %v", err)
	}
	if response.ID != "user-id" || response.Name != "Ada" || response.Description != "A profile" || response.AvatarURL != "/avatar.png" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func validPNG(t *testing.T) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageData.Set(0, 0, color.White)
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, imageData); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return buffer.Bytes()
}

func stringPointer(value string) *string {
	return &value
}
