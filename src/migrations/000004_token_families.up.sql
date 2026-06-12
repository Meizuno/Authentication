-- Token families for refresh-token reuse detection. A login starts a family;
-- each rotation issues a new token in the same family. Replay of an
-- already-used token revokes the whole family.
ALTER TABLE refresh_tokens ADD COLUMN family_id UUID;
UPDATE refresh_tokens SET family_id = uuid_generate_v4() WHERE family_id IS NULL;
ALTER TABLE refresh_tokens ALTER COLUMN family_id SET NOT NULL;

-- used_at NULL = live; non-NULL = already rotated (kept until expiry so a replay
-- is detectable rather than looking like an unknown token).
ALTER TABLE refresh_tokens ADD COLUMN used_at TIMESTAMPTZ NULL;

CREATE INDEX idx_refresh_tokens_family_id ON refresh_tokens(family_id);
