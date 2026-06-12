package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/myronovy/authentication/src/internal/config"
	"github.com/myronovy/authentication/src/internal/handler"
	repository "github.com/myronovy/authentication/src/internal/repository/postgres"
	"github.com/myronovy/authentication/src/internal/service"
	"github.com/myronovy/authentication/src/migrations"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	migrateOnly := flag.Bool("migrate-only", false, "apply database migrations and exit")
	flag.Parse()
	runMigrateOnly := *migrateOnly || os.Getenv("MIGRATE_ONLY") == "true"

	// Honor GIN_MODE if set; default to release for production safety.
	if mode := os.Getenv("GIN_MODE"); mode != "" {
		gin.SetMode(mode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	cfg := config.Load()

	// Run migrations from the embedded SQL (no dependency on CWD or shipped files).
	srcDriver, err := iofs.New(migrations.FS, ".")
	if err != nil {
		log.Fatalf("failed to load embedded migrations: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", srcDriver, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to init migrations: %v", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatalf("failed to run migrations: %v", err)
	}
	log.Println("Migrations applied")

	// Allow deployments to run migrations as a discrete step and exit cleanly.
	if runMigrateOnly {
		log.Println("migrate-only: migrations applied, exiting")
		return
	}

	// Connect to database
	db, err := gorm.Open(gormpostgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("failed to get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(3)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	log.Println("Database connected")

	// Wire up layers
	userRepo := repository.NewUserRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	authSvc := service.NewAuthService(cfg, userRepo, tokenRepo)
	authHandler := handler.NewAuthHandler(authSvc, cfg)
	router := handler.NewRouter(authHandler, authSvc)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
		// Bound every phase of a connection so a slow or idle client cannot
		// hold resources open (slowloris / exhaustion defense).
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Serve in the background so main can wait for a shutdown signal.
	go func() {
		log.Printf("Server starting on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("failed to start server: %v", err)
		}
	}()

	// Block until SIGINT/SIGTERM, then drain in-flight requests.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("server forced to shutdown: %v", err)
	}
	log.Println("Server exited")
}
