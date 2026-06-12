package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/myronovy/authentication/src/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type tokenRepository struct {
	db *gorm.DB
}

func NewTokenRepository(db *gorm.DB) domain.TokenRepository {
	return &tokenRepository{db: db}
}

func (r *tokenRepository) Create(ctx context.Context, token *domain.RefreshToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

func (r *tokenRepository) MarkUsed(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	var t domain.RefreshToken
	// UPDATE ... WHERE used_at IS NULL RETURNING is a single atomic statement:
	// Postgres row locking lets exactly one concurrent caller flip used_at
	// (RowsAffected == 1); the loser sees RowsAffected == 0 and gets (nil, nil).
	result := r.db.WithContext(ctx).
		Model(&t).
		Clauses(clause.Returning{}).
		Where("token_hash = ? AND used_at IS NULL", tokenHash).
		Update("used_at", time.Now())
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &t, nil
}

func (r *tokenRepository) FindByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	var t domain.RefreshToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *tokenRepository) DeleteByTokenHash(ctx context.Context, tokenHash string) error {
	return r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).Delete(&domain.RefreshToken{}).Error
}

func (r *tokenRepository) DeleteByUserID(ctx context.Context, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&domain.RefreshToken{}).Error
}

func (r *tokenRepository) DeleteByFamilyID(ctx context.Context, familyID uuid.UUID) error {
	return r.db.WithContext(ctx).Where("family_id = ?", familyID).Delete(&domain.RefreshToken{}).Error
}

func (r *tokenRepository) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	// A used-but-unexpired row is intentionally retained so a replay is still
	// detectable; once expired it is dead weight and safe to drop.
	result := r.db.WithContext(ctx).Where("expires_at < ?", now).Delete(&domain.RefreshToken{})
	return result.RowsAffected, result.Error
}
