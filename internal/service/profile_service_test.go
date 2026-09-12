package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

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

func TestProfileServiceUpdatesProfileAndAvatar(t *testing.T) {
	user := &model.User{ID: "user-id", Name: "Ada Lovelace", Description: "Old description", AvatarURL: "/old.png"}
	repo := &userRepositoryStub{existingUser: user}
	storage := &profileImageStorageStub{url: "/uploads/profile-images/user-id.png"}
	profile := NewProfileService(repo, storage)
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
			_, err := NewProfileService(repo, &profileImageStorageStub{url: "/new.png"}).Update(context.Background(), "user-id", test.request)
			if !errors.Is(err, test.want) {
				t.Fatalf("Update() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestProfileServiceNormalizesNameAndDescription(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Old"}}
	profile := NewProfileService(repo, &profileImageStorageStub{})
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
	_, err := NewProfileService(repo, &profileImageStorageStub{}).Update(context.Background(), "user-id", model.ProfileUpdateRequest{})
	if err == nil || err.Error() != "no hay cambios para guardar" {
		t.Fatalf("Update() error = %v, want no changes error", err)
	}
}

func TestProfileServicePropagatesRepositoryAndStorageErrors(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	storageErr := errors.New("storage unavailable")

	t.Run("get user", func(t *testing.T) {
		repo := &userRepositoryStub{getErr: databaseErr}
		_, err := NewProfileService(repo, &profileImageStorageStub{}).Update(context.Background(), "user-id", model.ProfileUpdateRequest{Name: stringPointer("Ada")})
		if !errors.Is(err, databaseErr) {
			t.Fatalf("Update() error = %v, want %v", err, databaseErr)
		}
	})

	t.Run("save image", func(t *testing.T) {
		repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada"}}
		storage := &profileImageStorageStub{saveErr: storageErr}
		_, err := NewProfileService(repo, storage).Update(context.Background(), "user-id", model.ProfileUpdateRequest{Image: validPNG(t), ImageContentType: "image/png", ImageProvided: true})
		if !errors.Is(err, storageErr) {
			t.Fatalf("Update() error = %v, want %v", err, storageErr)
		}
	})

	t.Run("update profile", func(t *testing.T) {
		repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada", AvatarURL: "/old.png"}, updateErr: databaseErr}
		storage := &profileImageStorageStub{url: "/new.png"}
		_, err := NewProfileService(repo, storage).Update(context.Background(), "user-id", model.ProfileUpdateRequest{Image: validPNG(t), ImageContentType: "image/png", ImageProvided: true})
		if !errors.Is(err, databaseErr) {
			t.Fatalf("Update() error = %v, want %v", err, databaseErr)
		}
		if storage.deleted != "/new.png" {
			t.Fatalf("new avatar = %q, want cleanup after persistence failure", storage.deleted)
		}
	})
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
