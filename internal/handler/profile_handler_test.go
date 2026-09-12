package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"
	"github.com/gin-gonic/gin"
)

type profileServiceStub struct {
	response *model.UserResponse
	err      error
	userID   string
	request  model.ProfileUpdateRequest
	called   bool
}

var _ service.ProfileService = (*profileServiceStub)(nil)

func (s *profileServiceStub) Update(_ context.Context, userID string, request model.ProfileUpdateRequest) (*model.UserResponse, error) {
	s.called = true
	s.userID = userID
	s.request = request
	return s.response, s.err
}

func (s *profileServiceStub) GetProfile(_ context.Context, userID string) (*model.UserResponse, error) {
	s.called = true
	s.userID = userID
	return s.response, s.err
}

func TestGetProfileHandlerReturnsProfile(t *testing.T) {
	serviceStub := &profileServiceStub{response: &model.UserResponse{ID: "user-id", Name: "Ada Lovelace", AvatarURL: "/uploads/user-id.png"}}
	recorder := performGetProfileRequest(t, serviceStub, "user-id")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !serviceStub.called || serviceStub.userID != "user-id" {
		t.Fatalf("service received user ID %q, want user-id", serviceStub.userID)
	}

	var response model.UserResponse
	decodeProfileJSON(t, recorder, &response)
	if response.ID != "user-id" || response.Name != "Ada Lovelace" || response.AvatarURL != "/uploads/user-id.png" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestGetProfileHandlerReturnsNotFound(t *testing.T) {
	serviceStub := &profileServiceStub{err: service.ErrUserNotFound}
	recorder := performGetProfileRequest(t, serviceStub, "user-id")

	assertProfileError(t, recorder, http.StatusNotFound, "USER_NOT_FOUND")
	if !serviceStub.called || serviceStub.userID != "user-id" {
		t.Fatalf("service received user ID %q, want user-id", serviceStub.userID)
	}
}

func TestGetProfileHandlerReturnsUnauthorized(t *testing.T) {
	serviceStub := &profileServiceStub{}
	recorder := performGetProfileRequestWithoutUser(t, serviceStub, "user-id")

	assertProfileError(t, recorder, http.StatusUnauthorized, "UNAUTHORIZED")
	if serviceStub.called {
		t.Fatal("service must not be called without authentication")
	}
}

func TestProfileHandlerUpdatesProfileFromMultipartRequest(t *testing.T) {
	serviceStub := &profileServiceStub{response: &model.UserResponse{ID: "user-id", Name: "Ada Lovelace", AvatarURL: "/uploads/user-id.png"}}
	recorder := performProfileRequest(t, serviceStub, "user-id", "Ada Lovelace", "Updated description", validProfilePNG(t))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !serviceStub.called || serviceStub.userID != "user-id" {
		t.Fatalf("service received user ID %q, want user-id", serviceStub.userID)
	}
	if serviceStub.request.Name == nil || *serviceStub.request.Name != "Ada Lovelace" {
		t.Fatalf("service received name = %v", serviceStub.request.Name)
	}
	if serviceStub.request.Description == nil || *serviceStub.request.Description != "Updated description" {
		t.Fatalf("service received description = %v", serviceStub.request.Description)
	}
	if !serviceStub.request.ImageProvided || serviceStub.request.ImageContentType != "image/png" || len(serviceStub.request.Image) == 0 {
		t.Fatal("service did not receive the uploaded PNG")
	}

	var response model.UserResponse
	decodeProfileJSON(t, recorder, &response)
	if response.AvatarURL != "/uploads/user-id.png" {
		t.Fatalf("avatar URL = %q", response.AvatarURL)
	}
}

func TestProfileHandlerRejectsMissingAuthentication(t *testing.T) {
	serviceStub := &profileServiceStub{}
	recorder := performProfileRequestWithoutUser(t, serviceStub, "user-id", "Ada", "", nil)

	assertProfileError(t, recorder, http.StatusUnauthorized, "UNAUTHORIZED")
	if serviceStub.called {
		t.Fatal("service must not be called without authentication")
	}
}

func TestProfileHandlerRejectsOversizedImageBeforeService(t *testing.T) {
	serviceStub := &profileServiceStub{}
	imageData := bytes.Repeat([]byte("x"), service.MaxProfileImageBytes+1)
	recorder := performProfileRequest(t, serviceStub, "user-id", "Ada", "", imageData)

	assertProfileError(t, recorder, http.StatusBadRequest, "INVALID_PROFILE")
	if serviceStub.called {
		t.Fatal("service must not be called for an oversized image")
	}
}

func TestProfileHandlerMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid profile", err: service.ErrInvalidProfileName, wantStatus: http.StatusBadRequest, wantCode: "INVALID_PROFILE"},
		{name: "missing user", err: service.ErrUserNotFound, wantStatus: http.StatusNotFound, wantCode: "USER_NOT_FOUND"},
		{name: "unexpected error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_SERVER_ERROR"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceStub := &profileServiceStub{err: test.err}
			recorder := performProfileRequest(t, serviceStub, "user-id", "Ada", "", nil)
			assertProfileError(t, recorder, test.wantStatus, test.wantCode)
		})
	}
}

func performProfileRequest(t *testing.T, profileService service.ProfileService, userID string, name string, description string, imageData []byte) *httptest.ResponseRecorder {
	return performProfileRequestWithAuth(t, profileService, userID, name, description, imageData, true)
}

func performProfileRequestWithoutUser(t *testing.T, profileService service.ProfileService, userID string, name string, description string, imageData []byte) *httptest.ResponseRecorder {
	return performProfileRequestWithAuth(t, profileService, userID, name, description, imageData, false)
}

func performProfileRequestWithAuth(t *testing.T, profileService service.ProfileService, userID string, name string, description string, imageData []byte, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	if name != "" {
		if err := multipartWriter.WriteField("name", name); err != nil {
			t.Fatal(err)
		}
	}
	if description != "" {
		if err := multipartWriter.WriteField("description", description); err != nil {
			t.Fatal(err)
		}
	}
	if imageData != nil {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="image"; filename="avatar.png"`)
		header.Set("Content-Type", "image/png")
		part, err := multipartWriter.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(imageData); err != nil {
			t.Fatal(err)
		}
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/v1/me/profile", &body)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	router := gin.New()
	router.PATCH("/v1/me/profile", func(c *gin.Context) {
		if authenticated {
			c.Set(middleware.UserIDKey, userID)
		}
		NewProfileHandler(profileService).Update(c)
	})
	router.ServeHTTP(recorder, request)
	return recorder
}

func assertProfileError(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d", recorder.Code, wantStatus)
	}
	var response model.ErrorResponse
	decodeProfileJSON(t, recorder, &response)
	if response.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q", response.Error.Code, wantCode)
	}
}

func decodeProfileJSON(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(recorder.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func validProfilePNG(t *testing.T) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageData.Set(0, 0, color.White)
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, imageData); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return buffer.Bytes()
}

func performGetProfileRequest(t *testing.T, profileService service.ProfileService, userID string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/me/profile", nil)
	router := gin.New()
	router.GET("/v1/me/profile", func(c *gin.Context) {
		c.Set(middleware.UserIDKey, userID)
		NewProfileHandler(profileService).GetProfile(c)
	})
	router.ServeHTTP(recorder, request)
	return recorder
}

func performGetProfileRequestWithoutUser(t *testing.T, profileService service.ProfileService, userID string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/me/profile", nil)
	router := gin.New()
	router.GET("/v1/me/profile", func(c *gin.Context) {
		NewProfileHandler(profileService).GetProfile(c)
	})
	router.ServeHTTP(recorder, request)
	return recorder
}
