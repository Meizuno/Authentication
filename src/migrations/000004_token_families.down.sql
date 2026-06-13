DROP INDEX IF EXISTS idx_refresh_tokens_family_id;
ALTER TABLE refresh_tokens DROP COLUMN used_at;
ALTER TABLE refresh_tokens DROP COLUMN family_id;
