-- +goose Up
-- Owned journal tables do not constrain ordinary Silo collection writers.
CREATE TABLE bloem_list_bindings (
 id uuid PRIMARY KEY,
 installation_id bigint NOT NULL REFERENCES bloem_managed_grants(installation_id) ON DELETE CASCADE,
 user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 profile_id text NOT NULL,
 subject jsonb NOT NULL,
 preview_id uuid NOT NULL UNIQUE,
 create_digest bytea NOT NULL CHECK(octet_length(create_digest)=32),
 source_id text NOT NULL,
 signing_key text NOT NULL CHECK(length(signing_key)=43),
 grant_cipher text NOT NULL,
 scope text NOT NULL CHECK(scope IN ('personal','server')),
 target_id text NOT NULL,
 library_ids integer[] NOT NULL DEFAULT '{}',
 generation bigint NOT NULL DEFAULT 1 CHECK(generation>0),
 status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','ready','failed','paused','detached')),
 last_success timestamptz,
 next_due timestamptz NOT NULL DEFAULT clock_timestamp(),
 applied_digest bytea CHECK(applied_digest IS NULL OR octet_length(applied_digest)=32),
 matched integer NOT NULL DEFAULT 0 CHECK(matched>=0),
 unmatched jsonb NOT NULL DEFAULT '[]',
 failure text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX bloem_list_personal_target ON bloem_list_bindings(user_id,target_id) WHERE scope='personal';
CREATE UNIQUE INDEX bloem_list_server_target ON bloem_list_bindings(target_id) WHERE scope='server';
CREATE INDEX bloem_list_due ON bloem_list_bindings(installation_id,next_due) WHERE status NOT IN ('detached','paused');
CREATE TABLE bloem_list_jobs (
 id uuid PRIMARY KEY,
 installation_id bigint NOT NULL REFERENCES bloem_managed_grants(installation_id) ON DELETE CASCADE,
 subject jsonb NOT NULL,
 mode text NOT NULL CHECK(mode IN ('resolve','refresh')),
 binding_id uuid REFERENCES bloem_list_bindings(id) ON DELETE CASCADE,
 generation bigint NOT NULL CHECK(generation>0),
 locator_cipher text NOT NULL DEFAULT '',
 lease_cipher text NOT NULL DEFAULT '',
 lease_digest bytea CHECK(lease_digest IS NULL OR octet_length(lease_digest)=32),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','running','resolved','complete','failed')),
 deadline timestamptz NOT NULL DEFAULT clock_timestamp()+interval '10 minutes',
 claimed_at timestamptz,
 resolution jsonb,
 grant_cipher text NOT NULL DEFAULT '',
 failure text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((mode='resolve' AND binding_id IS NULL) OR (mode='refresh' AND binding_id IS NOT NULL))
);
CREATE UNIQUE INDEX bloem_list_active_refresh ON bloem_list_jobs(binding_id) WHERE mode='refresh' AND state IN ('pending','running');
CREATE INDEX bloem_list_poll ON bloem_list_jobs(installation_id,created_at) WHERE state='pending';
CREATE TABLE bloem_list_pages (
 job_id uuid NOT NULL REFERENCES bloem_list_jobs(id) ON DELETE CASCADE,
 position integer NOT NULL CHECK(position>=0 AND position<=10000),
 payload_bytes integer NOT NULL CHECK(payload_bytes>0 AND payload_bytes<=8388608),
 page_cipher text NOT NULL,
 PRIMARY KEY(job_id,position)
);
CREATE TABLE bloem_list_receipts (
 job_id uuid PRIMARY KEY REFERENCES bloem_list_jobs(id) ON DELETE CASCADE,
 snapshot_id text NOT NULL,
 digest bytea NOT NULL CHECK(octet_length(digest)=32),
 receipt jsonb NOT NULL
);
CREATE TABLE bloem_list_request_intents (
 binding_id uuid NOT NULL REFERENCES bloem_list_bindings(id) ON DELETE CASCADE,
 identity_key text NOT NULL,
 entry jsonb NOT NULL,
 generation bigint NOT NULL CHECK(generation>0),
 wanted boolean NOT NULL DEFAULT true,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','running','blocked','delivered','cancelled')),
 reason text NOT NULL DEFAULT '',
 request_id text NOT NULL DEFAULT '',
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_due timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease_until timestamptz,
 PRIMARY KEY(binding_id,identity_key)
);
CREATE INDEX bloem_list_request_due ON bloem_list_request_intents(next_due) WHERE wanted AND status IN ('pending','running');

-- +goose Down
DROP TABLE bloem_list_request_intents;
DROP TABLE bloem_list_receipts;
DROP TABLE bloem_list_pages;
DROP TABLE bloem_list_jobs;
DROP TABLE bloem_list_bindings;
