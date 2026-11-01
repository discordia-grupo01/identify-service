package profileimage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	wantURL := server.URL + "/storage/v1/object/public/profile-images/user-id.png?v="
	if !strings.HasPrefix(imageURL, wantURL) {
		t.Fatalf("URL = %q, want prefix %q", imageURL, wantURL)
	}
	if err := storage.Delete(context.Background(), imageURL); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if len(requests) != 4 {
		t.Fatalf("request count = %d, want 4", len(requests))
	}
	if requests[0].method != http.MethodPost || requests[0].path != "/storage/v1/object/profile-images/user-id.png" || requests[0].body != "image bytes" {
		t.Fatalf("upload request = %+v", requests[0])
	}
	if requests[3].method != http.MethodDelete || requests[3].path != "/storage/v1/object/profile-images/user-id.png" {
		t.Fatalf("delete request = %+v", requests[3])
	}
	if requests[1].path != "/storage/v1/object/profile-images/user-id.jpg" || requests[2].path != "/storage/v1/object/profile-images/user-id.gif" {
		t.Fatalf("stale-format cleanup requests = %+v, %+v", requests[1], requests[2])
	}
}

// Al guardar una foto se borran los formatos viejos del usuario. Supabase Storage
// responde HTTP 400 con "statusCode":"404" en el cuerpo cuando el objeto no existe,
// y eso no puede hacer fallar la subida de la foto nueva.
func TestSupabaseStorageSaveIgnoresMissingStaleFormats(t *testing.T) {
	tests := []struct {
		name          string
		deleteStatus  int
		deleteBody    string
		wantSaveError bool
	}{
		{name: "supabase 400 con statusCode 404", deleteStatus: http.StatusBadRequest, deleteBody: `{"statusCode":"404","error":"not_found","message":"Object not found"}`},
		{name: "http 404", deleteStatus: http.StatusNotFound, deleteBody: `{"message":"Object not found"}`},
		{name: "error real de supabase", deleteStatus: http.StatusInternalServerError, deleteBody: `{"message":"boom"}`, wantSaveError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var deletedPaths []string
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodDelete {
					deletedPaths = append(deletedPaths, request.URL.Path)
					writer.Header().Set("Content-Type", "application/json")
					writer.WriteHeader(test.deleteStatus)
					_, _ = writer.Write([]byte(test.deleteBody))
					return
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
			if test.wantSaveError {
				if err == nil {
					t.Fatal("Save() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Save() error = %v, want nil", err)
			}
			if want := server.URL + "/storage/v1/object/public/profile-images/user-id.png?v="; !strings.HasPrefix(imageURL, want) {
				t.Fatalf("URL = %q, want prefix %q", imageURL, want)
			}
			for _, path := range deletedPaths {
				if path == "/storage/v1/object/profile-images/user-id.png" {
					t.Fatal("Save() borro la imagen que acaba de subir")
				}
			}
		})
	}
}

func TestNewSupabaseStorageRequiresConfiguration(t *testing.T) {
	if _, err := NewSupabaseStorage("", "service-role", "profile-images", ""); err == nil {
		t.Fatal("NewSupabaseStorage() error = nil, want configuration error")
	}
}
