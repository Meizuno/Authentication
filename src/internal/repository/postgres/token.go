package postgres

import (
	"context"

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

func (r *tokenRepository) ConsumeByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	var t domain.RefreshToken
	// DELETE ... RETURNING is a single atomic statement: Postgres row locking
	// guarantees only one concurrent caller deletes the row (RowsAffected == 1);
	// the loser sees RowsAffected == 0 and gets (nil, nil).
	result := r.db.WithContext(ctx).
		Clauses(clause.Returning{}).
		Where("token_hash = ?", tokenHash).
		Delete(&t)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &t, nil
}

func (r *tokenRepository) DeleteByTokenHash(ctx context.Context, tokenHash string) error {
	return r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).Delete(&domain.RefreshToken{}).Error
}

func (r *tokenRepository) DeleteByUserID(ctx context.Context, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&domain.RefreshToken{}).Error
}
