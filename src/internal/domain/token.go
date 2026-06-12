package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type RefreshToken struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;index"`
	User      User
	TokenHash string    `gorm:"column:token_hash;uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
}

type TokenRepository interface {
	Create(ctx context.Context, token *RefreshToken) error
	// ConsumeByTokenHash atomically deletes the row for tokenHash and returns
	// it. It returns (nil, nil) when no row matched, so concurrent refreshes of
	// the same token cannot both observe a live token.
	ConsumeByTokenHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	DeleteByTokenHash(ctx context.Context, tokenHash string) error
	DeleteByUserID(ctx context.Context, userID uuid.UUID) error
}
