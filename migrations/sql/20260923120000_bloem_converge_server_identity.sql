-- Bloem: converge the server identity onto upstream's row.
--
-- Bloem minted its stable server identifier under 'server.instance_id'
-- (internal/serverid). Upstream Silo later added the same concept under
-- 'server.identity_id' (internal/serveridentity, served by API v2). Two rows
-- would give one deployment two identities. internal/serverid now reads and
-- writes upstream's key; this copies an existing Bloem identifier across so
-- clients that already keyed their stored state on it keep recognising the
-- server. Bloem's value wins over any identity_id minted in between, because
-- that is the one clients hold.
--
-- +goose Up
INSERT INTO server_settings (key, value)
SELECT 'server.identity_id', value
FROM server_settings
WHERE key = 'server.instance_id' AND btrim(value) <> ''
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- +goose Down
-- No-op: rolling back must never change who the server is. The old row is
-- left in place untouched by Up, so Bloem's former key still holds the value.
SELECT 1;
