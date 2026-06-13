//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/myronovy/authentication/src/internal/domain"
	appmigrations "github.com/myronovy/authentication/src/migrations"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// setupDB starts a throwaway Postgres, applies the embedded migrations, and
// returns a gorm handle plus a seeded user id.
func setupDB(t *testing.T) (*gorm.DB, uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("authdb"),
		tcpostgres.WithUsername("authuser"),
		tcpostgres.WithPassword("authpass"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	src, err := iofs.New(appmigrations.FS, ".")
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		t.Fatalf("init migrate: %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	db, err := gorm.Open(gormpostgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}

	userID := uuid.New()
	if err := db.Exec(
		"INSERT INTO users (id, email) VALUES (?, ?)", userID, "user@example.com",
	).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return db, userID
}

func newToken(userID, familyID uuid.UUID, hash string, expiresAt time.Time) *domain.RefreshToken {
	return &domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: hash,
		FamilyID:  familyID,
		ExpiresAt: expiresAt,
	}
}

func TestMarkUsedIsSingleUse(t *testing.T) {
	db, userID := setupDB(t)
	repo := NewTokenRepository(db)
	ctx := context.Background()

	fam := uuid.New()
	if err := repo.Create(ctx, newToken(userID, fam, "hash-1", time.Now().Add(time.Hour))); err != nil {
		t.Fatalf("create: %v", err)
	}

	// First MarkUsed wins and returns the row with the family preserved.
	first, err := repo.MarkUsed(ctx, "hash-1")
	if err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}
	if first == nil || first.FamilyID != fam {
		t.Fatalf("first MarkUsed returned %+v, want family %s", first, fam)
	}

	// Second MarkUsed on the now-used row claims nothing.
	second, err := repo.MarkUsed(ctx, "hash-1")
	if err != nil {
		t.Fatalf("MarkUsed (2): %v", err)
	}
	if second != nil {
		t.Fatalf("second MarkUsed claimed an already-used token: %+v", second)
	}

	// The row still exists (retained for reuse detection) and is marked used.
	existing, err := repo.FindByTokenHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("FindByTokenHash: %v", err)
	}
	if existing == nil || existing.UsedAt == nil {
		t.Fatalf("expected a retained, used row, got %+v", existing)
	}
}

func TestDeleteByFamilyIDRevokesWholeFamily(t *testing.T) {
	db, userID := setupDB(t)
	repo := NewTokenRepository(db)
	ctx := context.Background()

	fam := uuid.New()
	other := uuid.New()
	exp := time.Now().Add(time.Hour)
	mustCreate(t, repo, newToken(userID, fam, "fam-a-1", exp))
	mustCreate(t, repo, newToken(userID, fam, "fam-a-2", exp))
	mustCreate(t, repo, newToken(userID, other, "fam-b-1", exp))

	if err := repo.DeleteByFamilyID(ctx, fam); err != nil {
		t.Fatalf("DeleteByFamilyID: %v", err)
	}

	if got, _ := repo.FindByTokenHash(ctx, "fam-a-1"); got != nil {
		t.Fatal("fam-a-1 should have been deleted")
	}
	if got, _ := repo.FindByTokenHash(ctx, "fam-b-1"); got == nil {
		t.Fatal("the other family must be untouched")
	}
}

func TestDeleteExpiredOnlyRemovesPastExpiry(t *testing.T) {
	db, userID := setupDB(t)
	repo := NewTokenRepository(db)
	ctx := context.Background()

	fam := uuid.New()
	mustCreate(t, repo, newToken(userID, fam, "expired", time.Now().Add(-time.Hour)))
	mustCreate(t, repo, newToken(userID, fam, "live", time.Now().Add(time.Hour)))

	n, err := repo.DeleteExpired(ctx, time.Now())
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if n != 1 {
		t.Fatalf("DeleteExpired removed %d rows, want 1", n)
	}
	if got, _ := repo.FindByTokenHash(ctx, "live"); got == nil {
		t.Fatal("live token must survive cleanup")
	}
}

func mustCreate(t *testing.T, repo domain.TokenRepository, tok *domain.RefreshToken) {
	t.Helper()
	if err := repo.Create(context.Background(), tok); err != nil {
		t.Fatalf("create %s: %v", tok.TokenHash, err)
	}
}
