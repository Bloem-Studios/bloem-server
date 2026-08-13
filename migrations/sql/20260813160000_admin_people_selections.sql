-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.admin_people_selections (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE RESTRICT,
    canonical_filter jsonb NOT NULL,
    snapshot_at timestamptz NOT NULL,
    account_ids integer[] NOT NULL,
    matched_count bigint NOT NULL CHECK (matched_count >= 0),
    excluded_count bigint NOT NULL DEFAULT 0 CHECK (excluded_count >= 0),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (matched_count = cardinality(account_ids))
);

CREATE INDEX admin_people_selections_expiry_idx
    ON public.admin_people_selections(expires_at);

CREATE FUNCTION public.reject_admin_people_selection_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' AND OLD.expires_at <= now() THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'admin people selections are immutable';
END;
$$;

CREATE TRIGGER admin_people_selections_immutable
BEFORE UPDATE OR DELETE ON public.admin_people_selections
FOR EACH ROW EXECUTE FUNCTION public.reject_admin_people_selection_mutation();

ALTER TABLE public.admin_audit_events
    DROP CONSTRAINT admin_audit_events_actor_platform_role_check,
    ADD CONSTRAINT admin_audit_events_actor_platform_role_check
        CHECK (actor_platform_role IN ('platform_admin', 'organization_admin')),
    DROP CONSTRAINT admin_audit_events_authority_context_check,
    ADD CONSTRAINT admin_audit_events_authority_context_check
        CHECK (authority_context IN ('platform', 'organization')),
    DROP CONSTRAINT admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN ('organization', 'membership', 'profile', 'bulk_job'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS admin_people_selections_immutable ON public.admin_people_selections;
DROP FUNCTION IF EXISTS public.reject_admin_people_selection_mutation();
DROP TABLE IF EXISTS public.admin_people_selections;

-- Organization-context events cannot be represented by the predecessor
-- schema. Remove them before restoring its narrower checks so rollback works
-- after the feature has processed real jobs.
DROP TRIGGER IF EXISTS admin_audit_events_immutable ON public.admin_audit_events;
DELETE FROM public.admin_audit_events
WHERE authority_context = 'organization'
   OR actor_platform_role = 'organization_admin'
   OR target_type IN ('profile', 'bulk_job');

ALTER TABLE public.admin_audit_events
    DROP CONSTRAINT admin_audit_events_actor_platform_role_check,
    ADD CONSTRAINT admin_audit_events_actor_platform_role_check
        CHECK (actor_platform_role = 'platform_admin'),
    DROP CONSTRAINT admin_audit_events_authority_context_check,
    ADD CONSTRAINT admin_audit_events_authority_context_check
        CHECK (authority_context = 'platform'),
    DROP CONSTRAINT admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN ('organization', 'membership'));

CREATE TRIGGER admin_audit_events_immutable
BEFORE UPDATE OR DELETE ON public.admin_audit_events
FOR EACH ROW EXECUTE FUNCTION public.reject_admin_audit_event_mutation();
-- +goose StatementEnd
