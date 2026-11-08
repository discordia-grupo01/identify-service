package main

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/audit"
	"github.com/discordia-grupo01/identify-service/internal/events"
	"github.com/discordia-grupo01/identify-service/internal/handler"
	"github.com/discordia-grupo01/identify-service/internal/logging"
	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/outbox"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/service"
	"github.com/discordia-grupo01/identify-service/internal/telemetry"
	emailutil "github.com/discordia-grupo01/identify-service/internal/utils/email"
	"github.com/discordia-grupo01/identify-service/internal/utils/googleoauth"
	"github.com/discordia-grupo01/identify-service/internal/utils/profileimage"
	"github.com/discordia-grupo01/identify-service/internal/utils/twofactor"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

// @title        Discordia - Identity
// @version      1.0.0
// @description  Identity/auth service: registration, login (password, Google OAuth, device PIN), sessions, password recovery, email confirmation, profile.
// @description  Routes under /v1/me and /v1/users require identity: the JWT is validated by Kong at the gateway (or by this service's own middleware.RequireAuth when hit directly).
// @description  An account suspended by platform staff (discordia-admin) gets 403 ACCOUNT_SUSPENDED on every login path, on POST /v1/refresh and on every authenticated route (even with a still-valid access token); its public profile (GET /v1/users/{id}) is 404.
// @BasePath     /
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 Type "Bearer" followed by a space and the JWT.
func main() {
	logging.MustSetup("identify-service")
	defer logging.Flush()

	environment := getEnv("ENVIRONMENT", "development")
	host := getEnv("HOST", "0.0.0.0")
	port := getEnv("PORT", "8080")

	if environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	shutdownTelemetry, err := telemetry.Setup(context.Background(), "identify-service")
	if err != nil {
		logging.Fatalf("telemetry: %v", err)
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(flushCtx)
	}()

	dbConnection := initDB()
	if dbConnection == nil {
		logging.Fatalf("failed to connect to the database")
	}
	defer func() {
		if err := dbConnection.Close(); err != nil {
			logging.Errorf("error closing database connection: %v", err)
		}
	}()

	router := gin.New()
	router.Use(telemetry.Middleware(), logging.Middleware(), logging.Recovery())

	userRepo := repository.NewUserRepository(dbConnection)
	refreshRepo := repository.NewRefreshTokenRepository(dbConnection)
	passwordRecoveryRepo := repository.NewPostgresPasswordRecoveryRepository(dbConnection)
	emailConfirmationRepo := repository.NewPostgresEmailConfirmationRepository(dbConnection)
	oauthIdentityRepo := repository.NewOAuthIdentityRepository(dbConnection)
	googleVerifier := googleoauth.NewVerifier(getEnv("GOOGLE_CLIENT_ID", ""))
	devicePinRepo := repository.NewDevicePinRepository(dbConnection)
	emailConfig, err := emailutil.ConfigFromEnv()
	if err != nil {
		logging.Fatalf("invalid email configuration: %v", err)
	}
	emailService, err := emailutil.NewSMTPService(emailConfig)
	if err != nil {
		logging.Fatalf("invalid email configuration: %v", err)
	}
	passwordRecoveryService := service.NewPasswordRecoveryService(passwordRecoveryRepo, userRepo, emailService)
	passwordRecoveryHandler := handler.NewPasswordRecoveryHandler(passwordRecoveryService)
	emailConfirmationService := service.NewEmailConfirmationService(emailConfirmationRepo, userRepo, emailService, getEnv("EMAIL_CONFIRMATION_URL", "http://localhost:3000/confirm-email"))
	emailConfirmationHandler := handler.NewEmailConfirmationHandler(emailConfirmationService)
	twoFactorCipher, err := twofactor.NewCipher(getEnv("TWO_FACTOR_ENCRYPTION_KEY", getEnv("JWT_SECRET", "")))
	if err != nil {
		logging.Fatalf("invalid two factor configuration: %v", err)
	}
	twoFactorRepo := repository.NewTwoFactorRepository(dbConnection)
	twoFactorService := service.NewTwoFactorService(twoFactorRepo, userRepo, refreshRepo, twoFactorCipher)
	twoFactorHandler := handler.NewTwoFactorHandler(twoFactorService)
	oauthService := service.NewOAuthService(userRepo, oauthIdentityRepo, refreshRepo, googleVerifier,
		service.WithOAuthTwoFactor(twoFactorService),
	)
	oauthHandler := handler.NewOAuthHandler(oauthService)
	pinService := service.NewPinService(devicePinRepo, userRepo, refreshRepo,
		service.WithPinTwoFactor(twoFactorService),
	)
	pinHandler := handler.NewPinHandler(pinService)

	loginAttemptRepo := repository.NewLoginAttemptRepository(dbConnection)
	authService := service.NewAuthService(userRepo, refreshRepo,
		service.WithEmailConfirmation(emailConfirmationService),
		service.WithTwoFactor(twoFactorService),
		service.WithLoginAttempts(loginAttemptRepo),
	)
	authHandler := handler.NewAuthHandler(authService)
	profileImageStorage := newProfileImageStorage()
	membershipRepo := repository.NewServerMembershipRepository(dbConnection)
	profileService := service.NewProfileService(userRepo, profileImageStorage, membershipRepo)
	profileHandler := handler.NewProfileHandler(profileService)
	if strings.EqualFold(getEnv("PROFILE_IMAGE_STORAGE", "local"), "local") {
		router.Static("/uploads/profile-images", getEnv("PROFILE_IMAGE_DIR", "./storage/profile-images"))
	}

	// RequireAuth consulta si la cuenta está suspendida; la caché de unos
	// segundos evita ir a la base en cada request, y el consumer de admin la
	// invalida apenas aplica un evento en esta instancia.
	suspensionCache := middleware.NewSuspensionCache(userRepo, 5*time.Second)

	// RabbitMQ es opcional: si no hay RABBITMQ_URL (ej. en un entorno sin la
	// dependencia levantada) el resto del servicio sigue funcionando
	// normalmente, solo sin "servidores en común" actualizados y con los
	// eventos de perfil esperando en la outbox.
	if rabbitMQURL := getEnv("RABBITMQ_URL", ""); rabbitMQURL != "" {
		// Sin el secreto no hay forma de verificar los admin.user_suspended /
		// admin.user_reactivated, y aceptarlos sin firma dejaría a cualquiera
		// con credenciales del broker suspender cuentas: no se arranca.
		adminSecret := getEnv("ADMIN_EVENTS_HMAC_SECRET", "")
		if adminSecret == "" {
			logging.Fatalf("ADMIN_EVENTS_HMAC_SECRET es obligatoria cuando RABBITMQ_URL está configurada: sin ella no se puede verificar la firma de los eventos de admin")
		}
		membershipHandler := events.NewMembershipHandler(membershipRepo)
		consumer := events.NewConsumer(rabbitMQURL, membershipHandler, audit.NewRecorder(dbConnection))
		go consumer.Start(context.Background())
		adminConsumer := events.NewAdminConsumer(rabbitMQURL, adminSecret, userRepo, audit.NewRecorder(dbConnection))
		adminConsumer.OnApplied(suspensionCache.Invalidate)
		go adminConsumer.Start(context.Background())
		// Sin el secreto de billing no se puede verificar billing.premium_updated:
		// el consumer no arranca y nadie pasa a Premium, pero el resto sigue
		// funcionando (no se tumba identify por una feature opcional).
		if billingSecret := getEnv("BILLING_EVENTS_HMAC_SECRET", ""); billingSecret != "" {
			go events.NewBillingConsumer(rabbitMQURL, billingSecret, userRepo, audit.NewRecorder(dbConnection)).Start(context.Background())
		} else {
			logging.Warnf("BILLING_EVENTS_HMAC_SECRET no configurada: no se inicia el consumer de billing (Premium no se actualiza)")
		}
		// El poller publica los users.profile_updated que dejan en la outbox
		// el registro, el alta por Google y los cambios de perfil y estado.
		go outbox.NewPoller(dbConnection, events.NewAMQPPublisher(rabbitMQURL)).Start(context.Background())
	} else {
		// Sin RabbitMQ los eventos quedan pendientes en outbox_events y se
		// publican cuando el servicio arranque con RABBITMQ_URL.
		logging.Warnf("RABBITMQ_URL no configurada: no se inician los consumers de membership y de admin ni el publisher de eventos")
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
		api.POST("/login/2fa", twoFactorHandler.VerifyLogin)
		api.POST("/oauth/google", oauthHandler.LoginWithGoogle)
		api.POST("/refresh", authHandler.Refresh)
		api.POST("/logout", middleware.RequireAuth(suspensionCache), authHandler.Logout)
		api.POST("/pin", middleware.RequireAuth(suspensionCache), pinHandler.Configure)
		api.POST("/pin/login", pinHandler.Login)
		api.POST("/password-recovery", passwordRecoveryHandler.RecoverPassword)
		api.POST("/password-reset", passwordRecoveryHandler.ResetPassword)
		api.POST("/email-confirmation", emailConfirmationHandler.Request)
		api.POST("/email-confirmation/confirm", emailConfirmationHandler.Confirm)
		api.POST("/me/password", middleware.RequireAuth(suspensionCache), authHandler.SetPassword)
		api.PATCH("/me/profile", middleware.RequireAuth(suspensionCache), profileHandler.Update)
		api.GET("/me/profile", middleware.RequireAuth(suspensionCache), profileHandler.GetProfile)
		api.GET("/users/:id", middleware.RequireAuth(suspensionCache), profileHandler.GetPublicProfile)
		api.PUT("/me/status", middleware.RequireAuth(suspensionCache), profileHandler.UpdateStatus)
		api.DELETE("/me/status", middleware.RequireAuth(suspensionCache), profileHandler.DeleteStatus)
		api.GET("/me/2fa", middleware.RequireAuth(suspensionCache), twoFactorHandler.Status)
		api.POST("/me/2fa/setup", middleware.RequireAuth(suspensionCache), twoFactorHandler.Setup)
		api.POST("/me/2fa/activate", middleware.RequireAuth(suspensionCache), twoFactorHandler.Activate)
		api.POST("/me/2fa/disable", middleware.RequireAuth(suspensionCache), twoFactorHandler.Disable)
		api.POST("/me/2fa/recovery-codes", middleware.RequireAuth(suspensionCache), twoFactorHandler.RegenerateRecoveryCodes)
	}

	addr := host + ":" + port
	logging.Infof("listening on %s", addr)
	logging.Fatalf("%v", router.Run(addr))
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
			logging.Fatalf("invalid Supabase image storage configuration: %v", err)
		}
		return storage
	default:
		logging.Fatalf("unsupported PROFILE_IMAGE_STORAGE %q: use local or supabase", getEnv("PROFILE_IMAGE_STORAGE", "local"))
		return nil
	}
}
