package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/myronovy/authentication/src/internal/config"
	"github.com/myronovy/authentication/src/internal/domain"
)

// fakeTokenRepo is an in-memory TokenRepository keyed by token hash. It lets the
// service tests observe exactly what gets persisted.
type fakeTokenRepo struct {
	mu     sync.Mutex
	byHash map[string]*domain.RefreshToken
}

func newFakeTokenRepo() *fakeTokenRepo {
	return &fakeTokenRepo{byHash: make(map[string]*domain.RefreshToken)}
}

func (r *fakeTokenRepo) Create(_ context.Context, t *domain.RefreshToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byHash[t.TokenHash] = t
	return nil
}

// MarkUsed mirrors the production atomic UPDATE ... WHERE used_at IS NULL: the
// map mutation is guarded so exactly one concurrent caller can claim a live row.
func (r *fakeTokenRepo) MarkUsed(_ context.Context, hash string) (*domain.RefreshToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.byHash[hash]
	if !ok || t.UsedAt != nil {
		return nil, nil
	}
	now := time.Now()
	t.UsedAt = &now
	return t, nil
}

func (r *fakeTokenRepo) FindByTokenHash(_ context.Context, hash string) (*domain.RefreshToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byHash[hash], nil
}

func (r *fakeTokenRepo) DeleteByTokenHash(_ context.Context, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byHash, hash)
	return nil
}

func (r *fakeTokenRepo) DeleteByUserID(_ context.Context, userID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for h, t := range r.byHash {
		if t.UserID == userID {
			delete(r.byHash, h)
		}
	}
	return nil
}

func (r *fakeTokenRepo) DeleteByFamilyID(_ context.Context, familyID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for h, t := range r.byHash {
		if t.FamilyID == familyID {
			delete(r.byHash, h)
		}
	}
	return nil
}

func (r *fakeTokenRepo) DeleteExpired(_ context.Context, now time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for h, t := range r.byHash {
		if t.ExpiresAt.Before(now) {
			delete(r.byHash, h)
			n++
		}
	}
	return n, nil
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

	pair, err := svc.generateTokenPair(context.Background(), userID, uuid.New())
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

func TestValidateAccessTokenRejectsTokenWithoutExp(t *testing.T) {
	svc := newTestService(newFakeTokenRepo(), newFakeUserRepo())

	// A correctly-signed token that simply omits exp must be rejected.
	claims := jwt.MapClaims{"sub": uuid.New().String(), "iat": time.Now().Unix()}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(svc.cfg.JWTSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := svc.ValidateAccessToken(signed); err == nil {
		t.Fatal("expected token without exp to be rejected")
	}
}

func TestValidateAccessTokenAcceptsTokenWithExp(t *testing.T) {
	svc := newTestService(newFakeTokenRepo(), newFakeUserRepo())
	userID := uuid.New()

	signed, err := svc.generateAccessToken(userID)
	if err != nil {
		t.Fatalf("generateAccessToken: %v", err)
	}

	got, err := svc.ValidateAccessToken(signed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != userID {
		t.Fatalf("user id = %s, want %s", got, userID)
	}
}

func TestFetchGoogleUserInfoNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	orig := googleUserInfoURL
	googleUserInfoURL = srv.URL
	defer func() { googleUserInfoURL = orig }()

	info, err := fetchGoogleUserInfo(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error on non-200, got nil")
	}
	if info != nil {
		t.Fatalf("expected nil info on error, got %+v", info)
	}
}

func TestFetchGoogleUserInfoSendsBearerHeaderNotQuery(t *testing.T) {
	var gotAuth, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":"u@example.com","verified_email":true,"name":"U"}`))
	}))
	defer srv.Close()

	orig := googleUserInfoURL
	googleUserInfoURL = srv.URL
	defer func() { googleUserInfoURL = orig }()

	info, err := fetchGoogleUserInfo(context.Background(), "good-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer good-token" {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer good-token")
	}
	if gotQuery != "" {
		t.Fatalf("token leaked into query string: %q", gotQuery)
	}
	if !info.VerifiedEmail || info.Email != "u@example.com" {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestRefreshHappyPathRotatesWithinFamily(t *testing.T) {
	tokenRepo := newFakeTokenRepo()
	svc := newTestService(tokenRepo, newFakeUserRepo())
	userID := uuid.New()
	familyID := uuid.New()

	pair, err := svc.generateTokenPair(context.Background(), userID, familyID)
	if err != nil {
		t.Fatalf("generateTokenPair: %v", err)
	}

	newPair, err := svc.RefreshTokens(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("RefreshTokens with raw token failed: %v", err)
	}
	if newPair.RefreshToken == pair.RefreshToken {
		t.Fatal("rotation returned the same refresh token")
	}

	// The old token is retained but marked used (kept for reuse detection); the
	// new token stays in the same family.
	old := tokenRepo.byHash[hashToken(pair.RefreshToken)]
	if old == nil || old.UsedAt == nil {
		t.Fatal("old token should be retained and marked used")
	}
	fresh := tokenRepo.byHash[hashToken(newPair.RefreshToken)]
	if fresh == nil || fresh.FamilyID != familyID {
		t.Fatalf("new token should belong to the same family %s", familyID)
	}
}

func TestRefreshReuseRevokesFamilyAndRejects(t *testing.T) {
	tokenRepo := newFakeTokenRepo()
	svc := newTestService(tokenRepo, newFakeUserRepo())
	familyID := uuid.New()

	first, err := svc.generateTokenPair(context.Background(), uuid.New(), familyID)
	if err != nil {
		t.Fatalf("generateTokenPair: %v", err)
	}

	// Legitimate rotation consumes `first` and issues `second` in the family.
	second, err := svc.RefreshTokens(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatalf("first rotation: %v", err)
	}

	// Replaying the already-used `first` token is a reuse event: reject AND
	// revoke the whole family, so `second` is killed too.
	if _, err := svc.RefreshTokens(context.Background(), first.RefreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("replay of used token: got %v, want ErrInvalidToken", err)
	}
	if len(tokenRepo.byHash) != 0 {
		t.Fatalf("family should be fully revoked, %d tokens remain", len(tokenRepo.byHash))
	}
	// The previously-valid `second` token must now be rejected too.
	if _, err := svc.RefreshTokens(context.Background(), second.RefreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second token after family revoke: got %v, want ErrInvalidToken", err)
	}
}

func TestRefreshUnknownTokenRejected(t *testing.T) {
	svc := newTestService(newFakeTokenRepo(), newFakeUserRepo())
	if _, err := svc.RefreshTokens(context.Background(), "never-issued"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown token: got %v, want ErrInvalidToken", err)
	}
}

func TestConcurrentRefreshAllowsOnlyOneWinner(t *testing.T) {
	tokenRepo := newFakeTokenRepo()
	svc := newTestService(tokenRepo, newFakeUserRepo())

	pair, err := svc.generateTokenPair(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("generateTokenPair: %v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	var successes int32
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := svc.RefreshTokens(context.Background(), pair.RefreshToken); err == nil {
				atomic.AddInt32(&successes, 1)
			}
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 refresh to win the race, got %d", successes)
	}
}
