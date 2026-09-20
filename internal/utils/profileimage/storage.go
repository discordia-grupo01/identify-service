package profileimage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Storage interface {
	Save(ctx context.Context, userID string, data []byte, contentType string) (string, error)
	Delete(ctx context.Context, imageURL string) error
}

type LocalStorage struct {
	directory string
	baseURL   string
}

func NewLocalStorage(directory string, baseURL string) *LocalStorage {
	return &LocalStorage{directory: directory, baseURL: strings.TrimRight(baseURL, "/")}
}

func imageExtension(contentType string) (string, bool) {
	extension, ok := map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/gif":  ".gif",
	}[contentType]
	return extension, ok
}

var profileImageExtensions = []string{".jpg", ".png", ".gif"}

func (s *LocalStorage) Save(ctx context.Context, userID string, data []byte, contentType string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	extension, ok := imageExtension(contentType)
	if !ok {
		return "", fmt.Errorf("unsupported image content type %q", contentType)
	}

	if err := os.MkdirAll(s.directory, 0o755); err != nil {
		return "", err
	}
	filename := userID + extension
	path := filepath.Join(s.directory, filename)
	temporary, err := os.CreateTemp(s.directory, ".profile-*")
	if err != nil {
		return "", err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)

	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return "", err
	}
	for _, otherExtension := range profileImageExtensions {
		if otherExtension == extension {
			continue
		}
		if err := os.Remove(filepath.Join(s.directory, userID+otherExtension)); err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}

	return s.baseURL + "/" + filename, nil
}

func (s *LocalStorage) Delete(ctx context.Context, imageURL string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	filename := filepath.Base(imageURL)
	if filename == "." || filename == string(filepath.Separator) || filename == "" {
		return nil
	}
	if err := os.Remove(filepath.Join(s.directory, filename)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
