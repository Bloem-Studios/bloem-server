-- +goose Up
ALTER TABLE bloem_book_requests
 DROP CONSTRAINT bloem_book_requests_fulfilment_check,
 ADD CONSTRAINT bloem_book_requests_fulfilment_check
 CHECK (fulfilment IN ('awaiting_fulfilment','library','manual','queued','searching','retrying','awaiting_library','failed'));

CREATE TABLE bloem_ebook_request_integrations (
 integration_id text PRIMARY KEY REFERENCES request_integrations(id) ON DELETE CASCADE,
 organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
 media_folder_id bigint NOT NULL REFERENCES media_folders(id) ON DELETE RESTRICT,
 revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
 updated_at timestamptz NOT NULL DEFAULT now()
);

-- The frozen descriptor contains resource identities and a configuration
-- digest only. Provider credentials and integration configuration stay outside.
CREATE TABLE bloem_ebook_request_dispatches (
 request_id uuid PRIMARY KEY REFERENCES bloem_book_requests(id) ON DELETE CASCADE,
 integration_id text NOT NULL,
 -- Refiling a cancelled ISBN retains the original provider submission identity.
 submission_request_id uuid NOT NULL,
 descriptor jsonb NOT NULL,
 started boolean NOT NULL DEFAULT false,
 external_id text NOT NULL DEFAULT '' CHECK (octet_length(external_id)<=8192),
 provider_status text NOT NULL DEFAULT 'unknown'
  CHECK (provider_status IN ('unknown','queued','searching','downloading','completed','failed')),
 failure_count integer NOT NULL DEFAULT 0 CHECK (failure_count>=0),
 lease_token uuid,
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);
CREATE INDEX bloem_ebook_request_dispatches_due
 ON bloem_ebook_request_dispatches(next_attempt_at,request_id);

CREATE INDEX bloem_ebook_request_acquisition_history
 ON bloem_book_requests(organization_id,isbn,id) WHERE media_type='book';

-- +goose Down
LOCK TABLE bloem_ebook_request_integrations, bloem_ebook_request_dispatches, bloem_book_requests IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM bloem_ebook_request_integrations)
 OR EXISTS(SELECT 1 FROM bloem_ebook_request_dispatches)
 OR EXISTS(SELECT 1 FROM bloem_book_requests WHERE fulfilment NOT IN ('awaiting_fulfilment','library','manual')) THEN
  RAISE EXCEPTION 'ebook fulfillment history retained: refusing destructive rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX bloem_ebook_request_acquisition_history;
DROP TABLE bloem_ebook_request_dispatches;
DROP TABLE bloem_ebook_request_integrations;
ALTER TABLE bloem_book_requests
 DROP CONSTRAINT bloem_book_requests_fulfilment_check,
 ADD CONSTRAINT bloem_book_requests_fulfilment_check CHECK (fulfilment IN ('awaiting_fulfilment','library','manual'));
