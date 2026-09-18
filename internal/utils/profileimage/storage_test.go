package profileimage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalStorageSaveWritesImageAndReturnsURL(t *testing.T) {
	directory := t.TempDir()
	storage := NewLocalStorage(directory, "/uploads/profile-images/")
	data := []byte("image bytes")

	imageURL, err := storage.Save(context.Background(), "user-id", data, "image/png")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if imageURL != "/uploads/profile-images/user-id.png" {
		t.Fatalf("URL = %q", imageURL)
	}
	stored, err := os.ReadFile(filepath.Join(directory, "user-id.png"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(stored) != string(data) {
		t.Fatalf("stored data = %q, want %q", stored, data)
	}
}

func TestLocalStorageUsesContentTypeExtensions(t *testing.T) {
	tests := map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/gif":  ".gif",
	}
	for contentType, extension := range tests {
		t.Run(contentType, func(t *testing.T) {
			storage := NewLocalStorage(t.TempDir(), "/images")
			if _, err := storage.Save(context.Background(), "user-id", []byte("data"), contentType); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			if _, err := os.Stat(filepath.Join(storage.directory, "user-id"+extension)); err != nil {
				t.Fatalf("expected %s file: %v", extension, err)
			}
		})
	}
}

func TestLocalStorageSaveRemovesPreviousImageFormat(t *testing.T) {
	storage := NewLocalStorage(t.TempDir(), "/images")
	if _, err := storage.Save(context.Background(), "user-id", []byte("jpg"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Save(context.Background(), "user-id", []byte("png"), "image/png"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(storage.directory, "user-id.jpg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old image still exists or unexpected error: %v", err)
	}
	stored, err := os.ReadFile(filepath.Join(storage.directory, "user-id.png"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(stored) != "png" {
		t.Fatalf("stored data = %q, want png", stored)
	}
}

func TestLocalStorageRejectsUnsupportedContentType(t *testing.T) {
	storage := NewLocalStorage(t.TempDir(), "/images")
	_, err := storage.Save(context.Background(), "user-id", []byte("data"), "image/webp")
	if err == nil || !strings.Contains(err.Error(), "unsupported image content type") {
		t.Fatalf("Save() error = %v, want unsupported content type error", err)
	}
}

func TestLocalStorageDeleteRemovesOnlyTheBaseFilename(t *testing.T) {
	directory := t.TempDir()
	storage := NewLocalStorage(directory, "/images")
	if _, err := storage.Save(context.Background(), "user-id", []byte("data"), "image/png"); err != nil {
		t.Fatal(err)
	}

	if err := storage.Delete(context.Background(), "https://cdn.example/avatar/user-id.png"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "user-id.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("image still exists or unexpected error: %v", err)
	}
}

func TestLocalStorageHonorsCanceledContext(t *testing.T) {
	storage := NewLocalStorage(t.TempDir(), "/images")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := storage.Save(ctx, "user-id", []byte("data"), "image/png"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save() error = %v, want context.Canceled", err)
	}
	if err := storage.Delete(ctx, "/images/user-id.png"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete() error = %v, want context.Canceled", err)
	}
}
