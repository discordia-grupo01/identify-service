package profileimage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSupabaseStorageSaveAndDelete(t *testing.T) {
	var requests []struct {
		method string
		path   string
		body   string
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		requests = append(requests, struct {
			method string
			path   string
			body   string
		}{request.Method, request.URL.Path, string(body)})
		if request.Header.Get("Authorization") != "Bearer service-role" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("apikey") != "service-role" {
			t.Errorf("apikey = %q", request.Header.Get("apikey"))
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	storage, err := NewSupabaseStorage(server.URL, "service-role", "profile-images", "")
	if err != nil {
		t.Fatalf("NewSupabaseStorage() error = %v", err)
	}
	storage.httpClient = server.Client()

	imageURL, err := storage.Save(context.Background(), "user-id", []byte("image bytes"), "image/png")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	wantURL := server.URL + "/storage/v1/object/public/profile-images/user-id.png"
	if imageURL != wantURL {
		t.Fatalf("URL = %q, want %q", imageURL, wantURL)
	}
	if err := storage.Delete(context.Background(), imageURL); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if requests[0].method != http.MethodPost || requests[0].path != "/storage/v1/object/profile-images/user-id.png" || requests[0].body != "image bytes" {
		t.Fatalf("upload request = %+v", requests[0])
	}
	if requests[1].method != http.MethodDelete || requests[1].path != "/storage/v1/object/profile-images/user-id.png" {
		t.Fatalf("delete request = %+v", requests[1])
	}
}

func TestNewSupabaseStorageRequiresConfiguration(t *testing.T) {
	if _, err := NewSupabaseStorage("", "service-role", "profile-images", ""); err == nil {
		t.Fatal("NewSupabaseStorage() error = nil, want configuration error")
	}
}
