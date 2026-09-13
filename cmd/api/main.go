package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/events"
	"github.com/discordia-grupo01/identify-service/internal/handler"
	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/service"
	emailutil "github.com/discordia-grupo01/identify-service/internal/utils/email"
	"github.com/discordia-grupo01/identify-service/internal/utils/profileimage"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

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
	sessionRepo := repository.NewSessionRepository(dbConnection)
	passwordRecoveryRepo := repository.NewPostgresPasswordRecoveryRepository(dbConnection)
	authService := service.NewAuthService(userRepo, sessionRepo)
	authHandler := handler.NewAuthHandler(authService)
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
	profileImageStorage := profileimage.NewLocalStorage(
		getEnv("PROFILE_IMAGE_DIR", "./storage/profile-images"),
		getEnv("PROFILE_IMAGE_URL", "/uploads/profile-images"),
	)
	profileService := service.NewProfileService(userRepo, profileImageStorage)
	profileHandler := handler.NewProfileHandler(profileService)
	router.Static("/uploads/profile-images", getEnv("PROFILE_IMAGE_DIR", "./storage/profile-images"))

	if rabbitMQURL := getEnv("RABBITMQ_URL", ""); rabbitMQURL != "" {
		membershipRepo := repository.NewServerMembershipRepository(dbConnection)
		membershipHandler := events.NewMembershipHandler(membershipRepo)
		consumer := events.NewConsumer(rabbitMQURL, membershipHandler)
		go consumer.Start(context.Background())
	} else {
		log.Println("RABBITMQ_URL no configurada: el consumer de membership no se inicia")
	}

	// /livez: solo confirma que el proceso esta vivo. No toca la BDD.
	router.GET("/livez", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// /readyz: confirma que el servicio puede operar (chequear BDD aca).
	router.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		// Verifica que la conexión a la base de datos responda
		if err := dbConnection.PingContext(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unhealthy",
				"error":  "database connection failed",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "discordia-SERVICIO up")
	})

	api := router.Group("/v1")
	{
		api.POST("/users", authHandler.Register)
		api.POST("/login", authHandler.Login)
		api.POST("/logout", middleware.RequireAuth(sessionRepo), authHandler.Logout)
		api.POST("/password-recovery", passwordRecoveryHandler.RecoverPassword)
		api.POST("/password-reset", passwordRecoveryHandler.ResetPassword)
		api.PATCH("/me/profile", middleware.RequireAuth(sessionRepo), profileHandler.Update)
		api.GET("/me/profile", middleware.RequireAuth(sessionRepo), profileHandler.GetProfile)
		api.GET("/users/:id", middleware.RequireAuth(sessionRepo), profileHandler.GetPublicProfile)
		api.PUT("/me/status", middleware.RequireAuth(sessionRepo), profileHandler.UpdateStatus)
		api.DELETE("/me/status", middleware.RequireAuth(sessionRepo), profileHandler.DeleteStatus)
	}

	addr := host + ":" + port
	log.Printf("listening on %s", addr)
	log.Fatal(router.Run(addr))
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
