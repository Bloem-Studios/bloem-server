-- +goose Up
-- The device a login session was opened from, as the client reported it in
-- X-Silo-Device-Id and X-Silo-Device-Platform. Audit and display data only:
-- the values are client-supplied and authorize nothing. NULL for sessions
-- opened before this migration or by clients that send no device headers.
-- Bloem: auth_sessions.device_id already exists (NOT NULL DEFAULT '', from
-- 20260813200000_profile_login_credentials) and binds direct-profile
-- sessions, so only device_platform is new here and Down keeps device_id.
ALTER TABLE auth_sessions
    ADD COLUMN IF NOT EXISTS device_id TEXT,
    ADD COLUMN device_platform TEXT;

-- +goose Down
ALTER TABLE auth_sessions
    DROP COLUMN device_platform;
