package profileimage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type SupabaseStorage struct {
	baseURL        string
	serviceRoleKey string
	bucket         string
	publicBaseURL  string
	httpClient     *http.Client
}

func NewSupabaseStorage(supabaseURL, serviceRoleKey, bucket, publicURL string) (*SupabaseStorage, error) {
	supabaseURL = strings.TrimRight(supabaseURL, "/")
	if supabaseURL == "" || serviceRoleKey == "" || bucket == "" {
		return nil, fmt.Errorf("supabase storage requires SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY and PROFILE_IMAGE_BUCKET")
	}
	if _, err := url.ParseRequestURI(supabaseURL); err != nil {
		return nil, fmt.Errorf("invalid SUPABASE_URL: %w", err)
	}

	publicBaseURL := strings.TrimRight(publicURL, "/")
	if publicBaseURL == "" {
		publicBaseURL = supabaseURL + "/storage/v1/object/public/" + bucket
	}

	return &SupabaseStorage{
		baseURL:        supabaseURL,
		serviceRoleKey: serviceRoleKey,
		bucket:         bucket,
		publicBaseURL:  publicBaseURL,
		httpClient:     http.DefaultClient,
	}, nil
}

func (s *SupabaseStorage) Save(ctx context.Context, userID string, data []byte, contentType string) (string, error) {
	extension, ok := imageExtension(contentType)
	if !ok {
		return "", fmt.Errorf("unsupported image content type %q", contentType)
	}

	objectPath := userID + extension
	requestURL := s.objectURL(objectPath)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	s.addHeaders(request, contentType)
	request.Header.Set("x-upsert", "true")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", responseError(response)
	}

	return s.publicBaseURL + "/" + objectPath, nil
}

func (s *SupabaseStorage) Delete(ctx context.Context, imageURL string) error {
	objectPath, ok := s.objectPath(imageURL)
	if !ok {
		return nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.objectURL(objectPath), nil)
	if err != nil {
		return err
	}
	s.addHeaders(request, "")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return responseError(response)
	}
	return nil
}

func (s *SupabaseStorage) objectURL(objectPath string) string {
	return s.baseURL + "/storage/v1/object/" + s.bucket + "/" + objectPath
}

func (s *SupabaseStorage) objectPath(imageURL string) (string, bool) {
	prefix := s.publicBaseURL + "/"
	if !strings.HasPrefix(imageURL, prefix) {
		return "", false
	}
	objectPath, err := url.PathUnescape(strings.TrimPrefix(imageURL, prefix))
	if err != nil || objectPath == "" || strings.Contains(objectPath, "..") {
		return "", false
	}
	return objectPath, true
}

func (s *SupabaseStorage) addHeaders(request *http.Request, contentType string) {
	request.Header.Set("Authorization", "Bearer "+s.serviceRoleKey)
	request.Header.Set("apikey", s.serviceRoleKey)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
}

func responseError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	return fmt.Errorf("supabase storage request failed with status %s: %s", response.Status, strings.TrimSpace(string(body)))
}
