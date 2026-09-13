// Package e2e contiene tests de punta a punta: arman el router real con
// repositorios reales contra un Postgres real (sin stubs), y lo ejercitan vía
// HTTP tal como lo haría un cliente real. Se saltean automáticamente si no
// hay DATABASE_HOST seteado en el entorno.
package e2e

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/handler"
	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/service"
	"github.com/discordia-grupo01/identify-service/internal/utils/profileimage"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

const testJWTSecret = "e2e-test-secret"

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	host := os.Getenv("DATABASE_HOST")
	if host == "" {
		t.Skip("DATABASE_HOST no está seteado: este test e2e requiere un Postgres real (docker compose up -d db && make migrate)")
	}

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host,
		getenvDefault("DATABASE_PORT", "5432"),
		getenvDefault("DATABASE_USER", "discordia"),
		getenvDefault("DATABASE_PASSWORD", "discordia"),
		getenvDefault("DATABASE_NAME", "discordia_servicio"),
		getenvDefault("DATABASE_SSLMODE", "disable"),
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("db.Ping() error = %v (¿corriste `make migrate`?)", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`TRUNCATE TABLE sessions, users RESTART IDENTITY CASCADE`)
		_ = db.Close()
	})

	if _, err := db.Exec(`TRUNCATE TABLE sessions, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	return db
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func newTestRouter(t *testing.T, db *sql.DB) *gin.Engine {
	t.Helper()
	t.Setenv("JWT_SECRET", testJWTSecret)

	gin.SetMode(gin.TestMode)
	router := gin.New()

	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewSessionRepository(db)

	authService := service.NewAuthService(userRepo, sessionRepo)
	authHandler := handler.NewAuthHandler(authService)

	profileStorage := profileimage.NewLocalStorage(t.TempDir(), "/uploads/profile-images")
	profileService := service.NewProfileService(userRepo, profileStorage)
	profileHandler := handler.NewProfileHandler(profileService)

	api := router.Group("/v1")
	{
		api.POST("/users", authHandler.Register)
		api.POST("/login", authHandler.Login)
		api.GET("/me/profile", middleware.RequireAuth(sessionRepo), profileHandler.GetProfile)
		api.PATCH("/me/profile", middleware.RequireAuth(sessionRepo), profileHandler.Update)
		api.GET("/users/:id", middleware.RequireAuth(sessionRepo), profileHandler.GetPublicProfile)
		api.PUT("/me/status", middleware.RequireAuth(sessionRepo), profileHandler.UpdateStatus)
		api.DELETE("/me/status", middleware.RequireAuth(sessionRepo), profileHandler.DeleteStatus)
	}

	return router
}

// registerAndLogin crea un usuario real (POST /v1/users) y lo loguea
// (POST /v1/login), devolviendo su id y un JWT real y válido.
func registerAndLogin(t *testing.T, router *gin.Engine, name string, email string, password string) (userID string, token string) {
	t.Helper()

	registerBody, _ := json.Marshal(model.RegisterRequest{Name: name, Email: email, Password: password})
	registerRecorder := doRequest(router, http.MethodPost, "/v1/users", registerBody, "")
	if registerRecorder.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", registerRecorder.Code, registerRecorder.Body.String())
	}
	var registered model.UserResponse
	decodeJSON(t, registerRecorder, &registered)

	loginBody, _ := json.Marshal(model.LoginRequest{Email: email, Password: password})
	loginRecorder := doRequest(router, http.MethodPost, "/v1/login", loginBody, "")
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRecorder.Code, loginRecorder.Body.String())
	}
	var authResponse model.AuthResponse
	decodeJSON(t, loginRecorder, &authResponse)

	return registered.ID, authResponse.Token
}

func doRequest(router *gin.Engine, method string, path string, body []byte, bearerToken string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeJSON(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(recorder.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v (body = %s)", err, recorder.Body.String())
	}
}
