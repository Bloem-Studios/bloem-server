-- +goose Up
CREATE TABLE bloem_book_requests (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    media_type text NOT NULL DEFAULT 'audiobook' CHECK (media_type = 'audiobook'),
    identity_key text NOT NULL,
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 500),
    author text NOT NULL DEFAULT '',
    isbn text NOT NULL DEFAULT '',
    asin text NOT NULL DEFAULT '',
    note text NOT NULL DEFAULT '',
    status text NOT NULL CHECK (status IN ('pending', 'approved', 'completed')),
    outcome text NOT NULL DEFAULT 'active' CHECK (outcome IN ('active', 'declined', 'cancelled')),
    outcome_reason text NOT NULL DEFAULT '',
    requested_by_user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_by_profile_id text NOT NULL,
    library_content_id text NOT NULL DEFAULT '',
    fulfilment text NOT NULL DEFAULT 'awaiting_fulfilment' CHECK (fulfilment IN ('awaiting_fulfilment', 'library', 'manual')),
    revision bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    fulfilled_notified_at timestamptz,
    client_key text NOT NULL,
    request_digest text NOT NULL,
    last_checked_at timestamptz
);
CREATE UNIQUE INDEX bloem_book_requests_idempotency
    ON bloem_book_requests(organization_id, requested_by_user_id, requested_by_profile_id, client_key);
CREATE UNIQUE INDEX bloem_book_requests_active_identity
    ON bloem_book_requests(organization_id, media_type, identity_key)
    WHERE outcome = 'active' AND status <> 'completed';
CREATE UNIQUE INDEX bloem_book_requests_active_isbn
    ON bloem_book_requests(organization_id, media_type, isbn)
    WHERE isbn <> '' AND outcome = 'active' AND status <> 'completed';
CREATE UNIQUE INDEX bloem_book_requests_active_asin
    ON bloem_book_requests(organization_id, media_type, asin)
    WHERE asin <> '' AND outcome = 'active' AND status <> 'completed';
CREATE INDEX bloem_book_requests_mine ON bloem_book_requests(organization_id, requested_by_user_id, requested_by_profile_id, created_at DESC, id);
CREATE INDEX bloem_book_requests_quota ON bloem_book_requests(requested_by_user_id, created_at) WHERE outcome <> 'declined';
CREATE INDEX bloem_book_requests_queue ON bloem_book_requests(organization_id, created_at DESC, id);

-- +goose Down
DROP TABLE bloem_book_requests;
