-- +goose Up
-- +goose StatementBegin
-- Tenant member create receipts are deliberately independent from both the
-- mutable membership and the global account. A command can therefore be
-- replayed after either resource changes or is removed without consuming a
-- second slot. request_hash is a bcrypt verifier for the canonical command;
-- no raw password or recoverable command body is retained.
CREATE TABLE public.tenant_member_command_receipts (
    organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    request_hash text NOT NULL,
    result_account_id integer NOT NULL,
    result_username text NOT NULL,
    result_email text NOT NULL,
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, idempotency_key)
);

CREATE INDEX tenant_member_command_receipts_result_idx
    ON public.tenant_member_command_receipts (organization_id, result_account_id);

-- Backfill the policy row every tenant needs. This is idempotent and covers
-- organizations created by older binaries before member administration was
-- deployed.
INSERT INTO public.access_groups (
    organization_id, name, description, is_default, library_ids,
    max_playback_quality, download_allowed, download_transcode_allowed,
    max_streams, max_transcodes, allowed_permissions, requests_allowed
)
SELECT o.id, 'Default Group', 'Applied automatically to newly created users.', true, NULL,
       '', true, false, 5, 5, ARRAY['marker_edit'], true
FROM public.organizations o
WHERE (o.external_service_id IS NOT NULL OR o.is_default)
  AND NOT EXISTS (
      SELECT 1 FROM public.access_groups g
      WHERE g.organization_id = o.id AND g.is_default
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.tenant_member_command_receipts_result_idx;
DROP TABLE IF EXISTS public.tenant_member_command_receipts;
-- Default groups are shared policy data and may have gained profile/user
-- references after this migration. Down intentionally leaves them intact.
-- +goose StatementEnd
