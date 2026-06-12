package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/myronovy/authentication/src/internal/config"
	"github.com/myronovy/authentication/src/internal/domain"
)

// fakeTokenRepo is an in-memory TokenRepository keyed by token hash. It lets the
// service tests observe exactly what gets persisted.
type fakeTokenRepo struct {
	byHash map[string]*domain.RefreshToken
}

func newFakeTokenRepo() *fakeTokenRepo {
	return &fakeTokenRepo{byHash: make(map[string]*domain.RefreshToken)}
}

func (r *fakeTokenRepo) Create(_ context.Context, t *domain.RefreshToken) error {
	r.byHash[t.TokenHash] = t
	return nil
}

func (r *fakeTokenRepo) FindByTokenHash(_ context.Context, hash string) (*domain.RefreshToken, error) {
	return r.byHash[hash], nil
}

func (r *fakeTokenRepo) DeleteByTokenHash(_ context.Context, hash string) error {
	delete(r.byHash, hash)
	return nil
}

func (r *fakeTokenRepo) DeleteByUserID(_ context.Context, userID uuid.UUID) error {
	for h, t := range r.byHash {
		if t.UserID == userID {
			delete(r.byHash, h)
		}
	}
	return nil
}

type fakeUserRepo struct {
	byID map[uuid.UUID]*domain.User
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{byID: make(map[uuid.UUID]*domain.User)}
}

func (r *fakeUserRepo) FindByEmail(_ context.Context, email string) (*domain.User, error) {
	for _, u := range r.byID {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, nil
}

func (r *fakeUserRepo) FindByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	return r.byID[id], nil
}

func (r *fakeUserRepo) Create(_ context.Context, u *domain.User) error {
	r.byID[u.ID] = u
	return nil
}

func newTestService(tokenRepo domain.TokenRepository, userRepo domain.UserRepository) *authService {
	cfg := &config.Config{JWTSecret: "test-secret-that-is-at-least-32-bytes-long"}
	return &authService{cfg: cfg, userRepo: userRepo, tokenRepo: tokenRepo}
}

func TestRefreshTokenStoredAsHashNotPlaintext(t *testing.T) {
	tokenRepo := newFakeTokenRepo()
	svc := newTestService(tokenRepo, newFakeUserRepo())
	userID := uuid.New()

	pair, err := svc.generateTokenPair(context.Background(), userID)
	if err != nil {
		t.Fatalf("generateTokenPair: %v", err)
	}

	// Exactly one token must have been persisted, and the raw token must not
	// appear as a stored key anywhere.
	if _, ok := tokenRepo.byHash[pair.RefreshToken]; ok {
		t.Fatal("raw refresh token was stored; it must only be stored as a hash")
	}
	if len(tokenRepo.byHash) != 1 {
		t.Fatalf("expected 1 stored token, got %d", len(tokenRepo.byHash))
	}

	wantHash := hashToken(pair.RefreshToken)
	stored, ok := tokenRepo.byHash[wantHash]
	if !ok {
		t.Fatal("token not stored under its SHA-256 hash")
	}
	if stored.TokenHash == pair.RefreshToken {
		t.Fatal("stored value equals the raw token; expected the hash")
	}
	if stored.TokenHash != wantHash {
		t.Fatalf("stored hash = %q, want %q", stored.TokenHash, wantHash)
	}
}

func TestRefreshLookupByRawTokenSucceeds(t *testing.T) {
	tokenRepo := newFakeTokenRepo()
	svc := newTestService(tokenRepo, newFakeUserRepo())
	userID := uuid.New()

	pair, err := svc.generateTokenPair(context.Background(), userID)
	if err != nil {
		t.Fatalf("generateTokenPair: %v", err)
	}

	// Presenting the raw token must resolve (the service hashes it on lookup),
	// and rotation must invalidate the old hash.
	newPair, err := svc.RefreshTokens(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("RefreshTokens with raw token failed: %v", err)
	}
	if newPair.RefreshToken == pair.RefreshToken {
		t.Fatal("rotation returned the same refresh token")
	}
	if _, ok := tokenRepo.byHash[hashToken(pair.RefreshToken)]; ok {
		t.Fatal("old token hash still present after rotation")
	}
}
