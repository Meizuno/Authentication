-- Replace the plaintext refresh-token column with a SHA-256 hash.
-- Existing rows cannot be migrated (the raw tokens are gone once hashed),
-- so we drop them; users simply re-login.
DELETE FROM refresh_tokens;

-- Dropping the column also drops its backing UNIQUE constraint/index
-- (refresh_tokens_token_key), so no separate DROP INDEX is needed — and
-- DROP INDEX on a constraint-owned index errors anyway.
ALTER TABLE refresh_tokens DROP COLUMN token;
ALTER TABLE refresh_tokens ADD COLUMN token_hash CHAR(64) NOT NULL;

CREATE UNIQUE INDEX idx_refresh_tokens_token_hash ON refresh_tokens(token_hash);
