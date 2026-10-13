-- +goose Up
CREATE TABLE bloem_managed_library_receipts (
 installation_id bigint NOT NULL REFERENCES bloem_managed_grants(installation_id) ON DELETE CASCADE,
 user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 profile_id text NOT NULL,
 event_id text NOT NULL,
 digest bytea NOT NULL CHECK(octet_length(digest)=32),
 receipt bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(installation_id,user_id,profile_id,event_id)
);
CREATE TABLE bloem_companion_owned_copies (
 user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 profile_id text NOT NULL,
 id text NOT NULL,
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 removed boolean NOT NULL DEFAULT false,
 changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(user_id,profile_id,id)
);
-- +goose Down
DROP TABLE bloem_companion_owned_copies;
DROP TABLE bloem_managed_library_receipts;
