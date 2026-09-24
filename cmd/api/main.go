package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/events"
	"github.com/discordia-grupo01/identify-service/internal/handler"
	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/service"
	emailutil "github.com/discordia-grupo01/identify-service/internal/utils/email"
	"github.com/discordia-grupo01/identify-service/internal/utils/googleoauth"
	"github.com/discordia-grupo01/identify-service/internal/utils/profileimage"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

// @title        Discordia - Identity
// @version      1.0.0
// @description  Identity/auth service: registration, login (password, Google OAuth, device PIN), sessions, password recovery, email confirmation, profile.
// @description  Routes under /v1/me and /v1/users require identity: the JWT is validated by Kong at the gateway (or by this service's own middleware.RequireAuth when hit directly).
// @BasePath     /
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 Type "Bearer" followed by a space and the JWT.
func main() {
	environment := getEnv("ENVIRONMENT", "development")
	host := getEnv("HOST", "0.0.0.0")
	port := getEnv("PORT", "8080")

	if environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	dbConnection := initDB()
	if dbConnection == nil {
		log.Fatal("failed to connect to the database")
	}
	defer func() {
		if err := dbConnection.Close(); err != nil {
			log.Printf("error closing database connection: %v", err)
		}
	}()

	router := gin.Default()

	userRepo := repository.NewUserRepository(dbConnection)
	refreshRepo := repository.NewRefreshTokenRepository(dbConnection)
	passwordRecoveryRepo := repository.NewPostgresPasswordRecoveryRepository(dbConnection)
	emailConfirmationRepo := repository.NewPostgresEmailConfirmationRepository(dbConnection)
	oauthIdentityRepo := repository.NewOAuthIdentityRepository(dbConnection)
	googleVerifier := googleoauth.NewVerifier(getEnv("GOOGLE_CLIENT_ID", ""))
	oauthService := service.NewOAuthService(userRepo, oauthIdentityRepo, refreshRepo, googleVerifier)
	oauthHandler := handler.NewOAuthHandler(oauthService)
	devicePinRepo := repository.NewDevicePinRepository(dbConnection)
	pinService := service.NewPinService(devicePinRepo, userRepo, refreshRepo)
	pinHandler := handler.NewPinHandler(pinService)
	emailConfig, err := emailutil.ConfigFromEnv()
	if err != nil {
		log.Fatalf("invalid email configuration: %v", err)
	}
	emailService, err := emailutil.NewSMTPService(emailConfig)
	if err != nil {
		log.Fatalf("invalid email configuration: %v", err)
	}
	passwordRecoveryService := service.NewPasswordRecoveryService(passwordRecoveryRepo, userRepo, emailService)
	passwordRecoveryHandler := handler.NewPasswordRecoveryHandler(passwordRecoveryService)
	emailConfirmationService := service.NewEmailConfirmationService(emailConfirmationRepo, userRepo, emailService, getEnv("EMAIL_CONFIRMATION_URL", "http://localhost:3000/confirm-email"))
	emailConfirmationHandler := handler.NewEmailConfirmationHandler(emailConfirmationService)
	authService := service.NewAuthService(userRepo, refreshRepo, emailConfirmationService)
	authHandler := handler.NewAuthHandler(authService)
	profileImageStorage := newProfileImageStorage()
	membershipRepo := repository.NewServerMembershipRepository(dbConnection)
	profileService := service.NewProfileService(userRepo, profileImageStorage, membershipRepo)
	profileHandler := handler.NewProfileHandler(profileService)
	if strings.EqualFold(getEnv("PROFILE_IMAGE_STORAGE", "local"), "local") {
		router.Static("/uploads/profile-images", getEnv("PROFILE_IMAGE_DIR", "./storage/profile-images"))
	}

	// El consumer de RabbitMQ es opcional: si no hay RABBITMQ_URL (ej. en un
	// entorno sin la dependencia levantada) el resto del servicio sigue
	// funcionando normalmente, solo sin "servidores en común" actualizados.
	if rabbitMQURL := getEnv("RABBITMQ_URL", ""); rabbitMQURL != "" {
		membershipHandler := events.NewMembershipHandler(membershipRepo)
		consumer := events.NewConsumer(rabbitMQURL, membershipHandler)
		go consumer.Start(context.Background())
	} else {
		log.Println("RABBITMQ_URL no configurada: el consumer de membership no se inicia")
	}

	router.GET("/livez", livez)
	router.GET("/readyz", readyz(dbConnection))

	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "discordia-SERVICIO up")
	})

	// Pagina de rebote para el login con Google desde Expo Go
	router.GET("/oauth/mobile-callback", handler.MobileOAuthCallback)

	api := router.Group("/v1")
	{
		api.POST("/users", authHandler.Register)
		api.POST("/login", authHandler.Login)
		api.POST("/oauth/google", oauthHandler.LoginWithGoogle)
		api.POST("/refresh", authHandler.Refresh)
		api.POST("/logout", middleware.RequireAuth(), authHandler.Logout)
		api.POST("/pin", middleware.RequireAuth(), pinHandler.Configure)
		api.POST("/pin/login", pinHandler.Login)
		api.POST("/password-recovery", passwordRecoveryHandler.RecoverPassword)
		api.POST("/password-reset", passwordRecoveryHandler.ResetPassword)
		api.POST("/email-confirmation", emailConfirmationHandler.Request)
		api.POST("/email-confirmation/confirm", emailConfirmationHandler.Confirm)
		api.PATCH("/me/profile", middleware.RequireAuth(), profileHandler.Update)
		api.GET("/me/profile", middleware.RequireAuth(), profileHandler.GetProfile)
		api.GET("/users/:id", middleware.RequireAuth(), profileHandler.GetPublicProfile)
		api.PUT("/me/status", middleware.RequireAuth(), profileHandler.UpdateStatus)
		api.DELETE("/me/status", middleware.RequireAuth(), profileHandler.DeleteStatus)
	}

	addr := host + ":" + port
	log.Printf("listening on %s", addr)
	log.Fatal(router.Run(addr))
}

// livez godoc
// @Summary  Liveness check
// @Tags     ops
// @Success  200  "ok"
// @Router   /livez [get]
func livez(c *gin.Context) { c.String(http.StatusOK, "ok") }

// readyz godoc
// @Summary      Readiness check
// @Description  Also pings the database.
// @Tags         ops
// @Produce      json
// @Success      200  "ready"
// @Failure      503  "database unavailable"
// @Router       /readyz [get]
func readyz(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unhealthy",
				"error":  "database connection failed",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func newProfileImageStorage() profileimage.Storage {
	switch strings.ToLower(getEnv("PROFILE_IMAGE_STORAGE", "local")) {
	case "local":
		return profileimage.NewLocalStorage(
			getEnv("PROFILE_IMAGE_DIR", "./storage/profile-images"),
			getEnv("PROFILE_IMAGE_URL", "/uploads/profile-images"),
		)
	case "supabase":
		storage, err := profileimage.NewSupabaseStorage(
			getEnv("SUPABASE_URL", ""),
			getEnv("SUPABASE_SERVICE_ROLE_KEY", ""),
			getEnv("PROFILE_IMAGE_BUCKET", "profile-images"),
			getEnv("PROFILE_IMAGE_PUBLIC_URL", ""),
		)
		if err != nil {
			log.Fatalf("invalid Supabase image storage configuration: %v", err)
		}
		return storage
	default:
		log.Fatalf("unsupported PROFILE_IMAGE_STORAGE %q: use local or supabase", getEnv("PROFILE_IMAGE_STORAGE", "local"))
		return nil
	}
}
