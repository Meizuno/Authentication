package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/myronovy/authentication/src/internal/config"
	"github.com/myronovy/authentication/src/internal/domain"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	accessTokenTTL  = 15 * time.Minute
	refreshTokenTTL = 7 * 24 * time.Hour
)

var (
	ErrEmailNotAllowed  = errors.New("email not allowed")
	ErrInvalidToken     = errors.New("invalid token")
	ErrTokenExpired     = errors.New("token expired")
)

type googleUserInfo struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

type AuthService interface {
	GetGoogleAuthURL(state string) string
	HandleGoogleCallback(ctx context.Context, code string) (*domain.TokenPair, error)
	RefreshTokens(ctx context.Context, refreshToken string) (*domain.TokenPair, error)
	ValidateAccessToken(tokenString string) (uuid.UUID, error)
	GetMe(ctx context.Context, userID uuid.UUID) (*domain.User, error)
}

type authService struct {
	cfg          *config.Config
	userRepo     domain.UserRepository
	tokenRepo    domain.TokenRepository
	oauthConfig  *oauth2.Config
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

	info, err := fetchGoogleUserInfo(oauthToken.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}

	if !s.isEmailAllowed(info.Email) {
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

	return s.generateTokenPair(ctx, user.ID)
}

func (s *authService) RefreshTokens(ctx context.Context, refreshToken string) (*domain.TokenPair, error) {
	stored, err := s.tokenRepo.FindByToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	if stored == nil || time.Now().After(stored.ExpiresAt) {
		return nil, ErrInvalidToken
	}

	if err := s.tokenRepo.DeleteByToken(ctx, refreshToken); err != nil {
		return nil, err
	}

	return s.generateTokenPair(ctx, stored.UserID)
}

func (s *authService) ValidateAccessToken(tokenString string) (uuid.UUID, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	})
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

func (s *authService) generateTokenPair(ctx context.Context, userID uuid.UUID) (*domain.TokenPair, error) {
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
		Token:     refreshToken,
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
	return base64.URLEncoding.EncodeToString(b), nil
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

func fetchGoogleUserInfo(accessToken string) (*googleUserInfo, error) {
	resp, err := http.Get("https://www.googleapis.com/oauth2/v2/userinfo?access_token=" + accessToken)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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
