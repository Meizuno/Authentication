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
	TokenHash string `gorm:"column:token_hash;uniqueIndex;not null"`
	// FamilyID groups every token issued from one login. Rotation keeps the
	// family; reuse of an already-rotated token revokes the whole family.
	FamilyID uuid.UUID `gorm:"type:uuid;not null;index"`
	// UsedAt is nil while the token is live and set when it has been rotated.
	UsedAt    *time.Time
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
}

type TokenRepository interface {
	Create(ctx context.Context, token *RefreshToken) error
	// MarkUsed atomically sets used_at on the live row for tokenHash and returns
	// it. It returns (nil, nil) when no live row matched (already used or
	// absent), so concurrent refreshes of the same token cannot both succeed.
	MarkUsed(ctx context.Context, tokenHash string) (*RefreshToken, error)
	// FindByTokenHash returns the row regardless of used_at, or (nil, nil) if
	// none exists. Used to distinguish a replayed (already-used) token from an
	// unknown one.
	FindByTokenHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	DeleteByTokenHash(ctx context.Context, tokenHash string) error
	DeleteByUserID(ctx context.Context, userID uuid.UUID) error
	// DeleteByFamilyID revokes an entire session (all tokens in a family).
	DeleteByFamilyID(ctx context.Context, familyID uuid.UUID) error
	// DeleteExpired removes rows past their expiry so the table stays bounded;
	// returns the number deleted.
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}
