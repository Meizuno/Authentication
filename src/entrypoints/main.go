package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	setupLogger()

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
		fatal("failed to load embedded migrations", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", srcDriver, cfg.DatabaseURL)
	if err != nil {
		fatal("failed to init migrations", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		fatal("failed to run migrations", err)
	}
	slog.Info("migrations applied")

	// Allow deployments to run migrations as a discrete step and exit cleanly.
	if runMigrateOnly {
		slog.Info("migrate-only: migrations applied, exiting")
		return
	}

	// Connect to database
	db, err := gorm.Open(gormpostgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		fatal("failed to connect to database", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		fatal("failed to get sql.DB", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(3)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	slog.Info("database connected")

	// Wire up layers
	userRepo := repository.NewUserRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	authSvc := service.NewAuthService(cfg, userRepo, tokenRepo)
	authHandler := handler.NewAuthHandler(authSvc, cfg)
	router := handler.NewRouter(authHandler, authSvc, cfg)

	// Periodically prune expired refresh tokens so the table stays bounded.
	cleanupCtx, stopCleanup := context.WithCancel(context.Background())
	defer stopCleanup()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-cleanupCtx.Done():
				return
			case <-ticker.C:
				if n, err := authSvc.CleanupExpiredTokens(cleanupCtx); err != nil {
					slog.Error("token cleanup failed", slog.Any("error", err))
				} else if n > 0 {
					slog.Info("token cleanup", slog.Int64("removed", n))
				}
			}
		}
	}()

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
		slog.Info("server starting", slog.String("port", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("failed to start server", err)
		}
	}()

	// Block until SIGINT/SIGTERM, then drain in-flight requests.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		fatal("server forced to shutdown", err)
	}
	slog.Info("server exited")
}

// setupLogger installs a JSON slog handler as the default, with level from
// LOG_LEVEL (debug|info|warn|error; default info).
func setupLogger() {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}

func fatal(msg string, err error) {
	slog.Error(msg, slog.Any("error", err))
	os.Exit(1)
}
