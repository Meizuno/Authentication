package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/myronovy/authentication/src/internal/audit"
	"github.com/myronovy/authentication/src/internal/config"
	"github.com/myronovy/authentication/src/internal/domain"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	accessTokenTTL  = 15 * time.Minute
	refreshTokenTTL = 7 * 24 * time.Hour
)

var (
	ErrEmailNotAllowed  = errors.New("email not allowed")
	ErrEmailNotVerified = errors.New("email not verified")
	ErrInvalidToken     = errors.New("invalid token")
	ErrTokenExpired     = errors.New("token expired")
)

type googleUserInfo struct {
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

// googleUserInfoURL is a var (not a const) so tests can point it at a stub
// server. googleHTTPClient bounds the outbound call so a hung Google response
// can't pin a request goroutine indefinitely.
var (
	googleUserInfoURL = "https://www.googleapis.com/oauth2/v2/userinfo"
	googleHTTPClient  = &http.Client{Timeout: 10 * time.Second}
)

type AuthService interface {
	GetGoogleAuthURL(state string) string
	HandleGoogleCallback(ctx context.Context, code string) (*domain.TokenPair, error)
	RefreshTokens(ctx context.Context, refreshToken string) (*domain.TokenPair, error)
	ValidateAccessToken(tokenString string) (uuid.UUID, error)
	GetMe(ctx context.Context, userID uuid.UUID) (*domain.User, error)
	Logout(ctx context.Context, refreshToken string) error
	LogoutAll(ctx context.Context, userID uuid.UUID) error
	CleanupExpiredTokens(ctx context.Context) (int64, error)
}

type authService struct {
	cfg         *config.Config
	userRepo    domain.UserRepository
	tokenRepo   domain.TokenRepository
	oauthConfig *oauth2.Config
}

func NewAuthService(cfg *config.Config, userRepo domain.UserRepository, tokenRepo domain.TokenRepository) AuthService {
	oauthConfig := &oauth2.Config{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: cfg.GoogleSecret,
		RedirectURL:  cfg.GoogleCallbackURL,
		Scopes:       []string{"email", "profile"},
		Endpoint:     google.Endpoint,
	}

	return &authService{
		cfg:         cfg,
		userRepo:    userRepo,
		tokenRepo:   tokenRepo,
		oauthConfig: oauthConfig,
	}
}

func (s *authService) GetGoogleAuthURL(state string) string {
	return s.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func (s *authService) HandleGoogleCallback(ctx context.Context, code string) (*domain.TokenPair, error) {
	oauthToken, err := s.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}

	info, err := fetchGoogleUserInfo(ctx, oauthToken.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}

	if info.Email == "" || !info.VerifiedEmail {
		audit.Event(ctx, "login", "denied_email_not_verified", slog.String("email", info.Email))
		return nil, ErrEmailNotVerified
	}

	if !s.isEmailAllowed(info.Email) {
		audit.Event(ctx, "login", "denied_email_not_allowed", slog.String("email", info.Email))
		return nil, ErrEmailNotAllowed
	}

	user, err := s.userRepo.FindByEmail(ctx, info.Email)
	if err != nil {
		return nil, err
	}

	if user == nil {
		user = &domain.User{
			ID:        uuid.New(),
			Email:     info.Email,
			Name:      info.Name,
			AvatarURL: info.Picture,
		}
		if err := s.userRepo.Create(ctx, user); err != nil {
			return nil, err
		}
	}

	// A fresh login starts a new token family.
	pair, err := s.generateTokenPair(ctx, user.ID, uuid.New())
	if err != nil {
		return nil, err
	}
	audit.Event(ctx, "login", "success",
		slog.String("user_id", user.ID.String()), slog.String("email", user.Email))
	return pair, nil
}

func (s *authService) RefreshTokens(ctx context.Context, refreshToken string) (*domain.TokenPair, error) {
	tokenHash := hashToken(refreshToken)

	// Atomically claim the live token (mark-used). Exactly one concurrent
	// refresh can win, so a token can never be rotated twice.
	used, err := s.tokenRepo.MarkUsed(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if used != nil {
		if time.Now().After(used.ExpiresAt) {
			return nil, ErrInvalidToken
		}
		// Rotation stays within the same family.
		pair, err := s.generateTokenPair(ctx, used.UserID, used.FamilyID)
		if err != nil {
			return nil, err
		}
		audit.Event(ctx, "token_refresh", "success", slog.String("user_id", used.UserID.String()))
		return pair, nil
	}

	// No live row claimed. If the token nonetheless exists, it was already
	// rotated — a replay of a stolen or superseded token. Revoke the whole
	// family (session) and reject.
	existing, err := s.tokenRepo.FindByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if delErr := s.tokenRepo.DeleteByFamilyID(ctx, existing.FamilyID); delErr != nil {
			return nil, delErr
		}
		// Audit without secrets — never log the token or its hash.
		audit.Event(ctx, "token_refresh", "reuse_detected",
			slog.String("user_id", existing.UserID.String()),
			slog.String("family_id", existing.FamilyID.String()))
		return nil, ErrInvalidToken
	}

	return nil, ErrInvalidToken
}

// CleanupExpiredTokens deletes refresh tokens past their expiry. Safe to call
// periodically; returns the number removed.
func (s *authService) CleanupExpiredTokens(ctx context.Context) (int64, error) {
	return s.tokenRepo.DeleteExpired(ctx, time.Now())
}

func (s *authService) ValidateAccessToken(tokenString string) (uuid.UUID, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	}, jwt.WithExpirationRequired())
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return uuid.Nil, ErrTokenExpired
		}
		return uuid.Nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return uuid.Nil, ErrInvalidToken
	}

	sub, ok := claims["sub"].(string)
	if !ok {
		return uuid.Nil, ErrInvalidToken
	}

	userID, err := uuid.Parse(sub)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}

	return userID, nil
}

// Logout revokes the single presented refresh token.
func (s *authService) Logout(ctx context.Context, refreshToken string) error {
	return s.tokenRepo.DeleteByTokenHash(ctx, hashToken(refreshToken))
}

// LogoutAll revokes every refresh token belonging to the user.
func (s *authService) LogoutAll(ctx context.Context, userID uuid.UUID) error {
	return s.tokenRepo.DeleteByUserID(ctx, userID)
}

func (s *authService) GetMe(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrInvalidToken
	}
	return user, nil
}

// generateTokenPair issues an access token plus a refresh token recorded in the
// given family. A fresh login passes a new family id; a rotation reuses the
// rotated token's family.
func (s *authService) generateTokenPair(ctx context.Context, userID, familyID uuid.UUID) (*domain.TokenPair, error) {
	accessToken, err := s.generateAccessToken(userID)
	if err != nil {
		return nil, err
	}

	refreshToken, err := s.generateRefreshToken()
	if err != nil {
		return nil, err
	}

	stored := &domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: hashToken(refreshToken),
		FamilyID:  familyID,
		ExpiresAt: time.Now().Add(refreshTokenTTL),
	}
	if err := s.tokenRepo.Create(ctx, stored); err != nil {
		return nil, err
	}

	return &domain.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func (s *authService) generateAccessToken(userID uuid.UUID) (string, error) {
	claims := jwt.MapClaims{
		"sub": userID.String(),
		"exp": time.Now().Add(accessTokenTTL).Unix(),
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.cfg.JWTSecret))
}

func generateRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken returns the hex-encoded SHA-256 of a raw refresh token. Only this
// hash is ever persisted; the raw token is shown to the client once and never
// stored, so a database leak cannot be replayed against /refresh.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *authService) generateRefreshToken() (string, error) {
	return generateRefreshToken()
}

func (s *authService) isEmailAllowed(email string) bool {
	if len(s.cfg.AllowedEmails) == 0 {
		return true
	}
	for _, allowed := range s.cfg.AllowedEmails {
		if strings.EqualFold(allowed, email) {
			return true
		}
	}
	return false
}

func fetchGoogleUserInfo(ctx context.Context, accessToken string) (*googleUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	// Pass the token in the Authorization header, not the query string, so it
	// does not land in access logs along the way.
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := googleHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google userinfo returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var info googleUserInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}
	return &info, nil
}
