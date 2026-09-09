package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/handler"
	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/service"

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
	authService := service.NewAuthService(userRepo, sessionRepo)
	authHandler := handler.NewAuthHandler(authService)

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
