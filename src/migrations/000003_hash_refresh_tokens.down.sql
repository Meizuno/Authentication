DELETE FROM refresh_tokens;

DROP INDEX IF EXISTS idx_refresh_tokens_token_hash;
ALTER TABLE refresh_tokens DROP COLUMN token_hash;
ALTER TABLE refresh_tokens ADD COLUMN token VARCHAR(255) NOT NULL UNIQUE;
