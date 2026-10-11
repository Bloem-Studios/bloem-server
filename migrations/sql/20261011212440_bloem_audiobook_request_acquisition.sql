-- +goose Up
-- Automatic audiobook acquisition. bloem_ebook_request_dispatches is a
-- historical name: the dispatch table already accepts every book media type.
ALTER TABLE bloem_book_requests
 DROP CONSTRAINT bloem_book_requests_fulfilment_check,
 ADD CONSTRAINT bloem_book_requests_fulfilment_check
 CHECK (fulfilment IN ('awaiting_fulfilment','library','manual','queued','searching','downloading','retrying','awaiting_library','review','failed'));
ALTER TABLE bloem_ebook_request_dispatches
 DROP CONSTRAINT bloem_ebook_request_dispatches_provider_status_check,
 ADD CONSTRAINT bloem_ebook_request_dispatches_provider_status_check
 CHECK (provider_status IN ('unknown','queued','searching','downloading','completed','failed','review'));

-- Binding of an audiobook request integration to the library its downloads
-- land in. media_folder_id cascades (unlike the ebook binding's RESTRICT) so a
-- binding never blocks a library delete; in-flight dispatches keep their
-- frozen roots and surface as stuck.
CREATE TABLE bloem_audiobook_request_integrations (
 integration_id text PRIMARY KEY REFERENCES request_integrations(id) ON DELETE CASCADE,
 organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
 media_folder_id bigint NOT NULL REFERENCES media_folders(id) ON DELETE CASCADE,
 download_roots text[] NOT NULL CHECK (cardinality(download_roots) BETWEEN 1 AND 4),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
 updated_at timestamptz NOT NULL DEFAULT now()
);

-- Per-attempt acquisition state, 1:1 with an audiobook dispatch row.
CREATE TABLE bloem_audiobook_request_acquisitions (
 request_id uuid PRIMARY KEY REFERENCES bloem_ebook_request_dispatches(request_id) ON DELETE CASCADE,
 attempt integer NOT NULL DEFAULT 1 CHECK (attempt BETWEEN 1 AND 1000),
 attempt_started_at timestamptz NOT NULL DEFAULT now(),
 search_passes integer NOT NULL DEFAULT 0 CHECK (search_passes>=0),
 absent_checks integer NOT NULL DEFAULT 0 CHECK (absent_checks>=0),
 ai_calls integer NOT NULL DEFAULT 0 CHECK (ai_calls>=0),
 external_status text NOT NULL DEFAULT '' CHECK (octet_length(external_status)<=32),
 review_kind text CHECK (review_kind IN ('selection','verification')),
 approved_candidate text CHECK (octet_length(approved_candidate)<=300),
 -- Set when provider_status becomes completed; drives the 24-hour stuck rule.
 provider_completed_at timestamptz,
 attention_notified_attempt integer NOT NULL DEFAULT 0 CHECK (attention_notified_attempt>=0),
 updated_at timestamptz NOT NULL DEFAULT now()
);
-- Per-submission in-flight check before a refile may start a provider call.
CREATE INDEX bloem_audiobook_request_dispatch_submission
 ON bloem_ebook_request_dispatches(submission_request_id);

-- Append-only decision log shown to administrators, trimmed to the newest 50
-- rows per request on insert. Envelopes are re-serialized from validated
-- types; raw plugin text is never stored.
CREATE TABLE bloem_audiobook_request_decisions (
 id bigserial PRIMARY KEY,
 request_id uuid NOT NULL REFERENCES bloem_book_requests(id) ON DELETE CASCADE,
 attempt integer NOT NULL CHECK (attempt>0),
 phase text NOT NULL CHECK (phase IN ('select','verify','admin')),
 outcome text NOT NULL CHECK (outcome IN ('handed_off','adopted','no_match','held','rejected','verified','review','unavailable','admin_approved','admin_accepted')),
 envelope jsonb NOT NULL CHECK (octet_length(envelope::text)<=8192),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bloem_audiobook_request_decisions_request
 ON bloem_audiobook_request_decisions(request_id,id DESC);
CREATE INDEX bloem_audiobook_request_acquisition_history
 ON bloem_book_requests(organization_id,identity_key,id) WHERE media_type='audiobook';

-- +goose Down
LOCK TABLE bloem_audiobook_request_decisions, bloem_audiobook_request_acquisitions,
 bloem_audiobook_request_integrations, bloem_ebook_request_dispatches, bloem_book_requests IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM bloem_audiobook_request_decisions)
 OR EXISTS(SELECT 1 FROM bloem_audiobook_request_acquisitions)
 OR EXISTS(SELECT 1 FROM bloem_audiobook_request_integrations)
 OR EXISTS(SELECT 1 FROM bloem_book_requests WHERE fulfilment IN ('downloading','review'))
 OR EXISTS(SELECT 1 FROM bloem_ebook_request_dispatches WHERE provider_status='review') THEN
  RAISE EXCEPTION 'audiobook acquisition history retained: refusing destructive rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX bloem_audiobook_request_acquisition_history;
DROP TABLE bloem_audiobook_request_decisions;
DROP INDEX bloem_audiobook_request_dispatch_submission;
DROP TABLE bloem_audiobook_request_acquisitions;
DROP TABLE bloem_audiobook_request_integrations;
ALTER TABLE bloem_ebook_request_dispatches
 DROP CONSTRAINT bloem_ebook_request_dispatches_provider_status_check,
 ADD CONSTRAINT bloem_ebook_request_dispatches_provider_status_check
 CHECK (provider_status IN ('unknown','queued','searching','downloading','completed','failed'));
ALTER TABLE bloem_book_requests
 DROP CONSTRAINT bloem_book_requests_fulfilment_check,
 ADD CONSTRAINT bloem_book_requests_fulfilment_check
 CHECK (fulfilment IN ('awaiting_fulfilment','library','manual','queued','searching','retrying','awaiting_library','failed'));
