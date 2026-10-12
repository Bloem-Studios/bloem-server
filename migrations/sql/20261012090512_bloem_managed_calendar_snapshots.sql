-- +goose Up
CREATE TABLE bloem_managed_calendar_snapshots (
 id uuid PRIMARY KEY,
 revision bigserial UNIQUE NOT NULL,
 installation_id bigint NOT NULL REFERENCES bloem_managed_grants(installation_id) ON DELETE CASCADE,
 account_id integer NOT NULL,
 profile_id text NOT NULL,
 account_revision bigint NOT NULL CHECK(account_revision>=0),
 profile_revision bigint NOT NULL CHECK(profile_revision>=0),
 start_date date NOT NULL,
 end_date date NOT NULL CHECK(end_date>=start_date AND end_date-start_date<=30),
 visibility_digest text NOT NULL CHECK(length(visibility_digest) BETWEEN 1 AND 256),
 payload bytea NOT NULL CHECK(octet_length(payload)<=8388608),
 page_size integer NOT NULL CHECK(page_size BETWEEN 1 AND 100),
 next_offset integer NOT NULL DEFAULT 0 CHECK(next_offset BETWEEN 0 AND 10000),
 expires_at timestamptz NOT NULL DEFAULT (clock_timestamp()+interval '120 seconds'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX bloem_managed_calendar_snapshots_expiry ON bloem_managed_calendar_snapshots(expires_at);

-- +goose Down
DROP TABLE bloem_managed_calendar_snapshots;
