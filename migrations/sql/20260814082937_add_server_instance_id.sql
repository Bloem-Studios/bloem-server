-- Reserves the plain server_settings row 'server.instance_id', which holds the
-- stable identifier GET /api/v1/server/identity serves and clients key their
-- stored per-server state on.
--
-- +goose Up
-- Deliberately inserts nothing. The identifier is a random UUID minted on the
-- first read of the setting, through SetIfAbsent, so an upgraded install and a
-- fresh install take the identical path and neither ends up with an identity
-- that a later migration or a re-run could replace. Seeding it here would need
-- to generate a value per database, which is exactly the write that must happen
-- once and never again.
--
-- The row is NOT encrypted: it is public by design (every client is told the
-- value) and encrypted settings are GCM-bound to their key name, which would
-- make it unreadable after any future rename.
SELECT 1;

-- +goose Down
-- Also a no-op. Deleting the row would re-key every client that has already
-- stored state against this server the next time the identifier is minted, and
-- rolling a schema change back is never a reason to change who the server is.
SELECT 1;
