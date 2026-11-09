package profileimage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
		"image/webp": ".webp",
		"image/gif":  ".gif",
	}[contentType]
	return extension, ok
}

var profileImageExtensions = []string{".jpg", ".png", ".webp", ".gif"}

const versionParam = "v"

func versionedURL(imageURL string) string {
	return imageURL + "?" + versionParam + "=" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// WithoutVersion devuelve la URL sin query string (sin el ?v= de Save).
func WithoutVersion(imageURL string) string {
	if index := strings.IndexByte(imageURL, '?'); index >= 0 {
		return imageURL[:index]
	}
	return imageURL
}

// SameObject dice si dos URLs devueltas por Save apuntan al mismo archivo
// guardado, aunque tengan distinta version.
func SameObject(firstURL, secondURL string) bool {
	return WithoutVersion(firstURL) == WithoutVersion(secondURL)
}

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

	return versionedURL(s.baseURL + "/" + filename), nil
}

func (s *LocalStorage) Delete(ctx context.Context, imageURL string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	filename := filepath.Base(WithoutVersion(imageURL))
	if filename == "." || filename == string(filepath.Separator) || filename == "" {
		return nil
	}
	if err := os.Remove(filepath.Join(s.directory, filename)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
