-- +goose Up
-- +goose StatementBegin
-- The support routes audit sessions, devices, streams and support access, and
-- an event written under break-glass access names the access it ran under.
ALTER TABLE public.admin_audit_events
    ADD COLUMN support_access_id uuid REFERENCES public.bloem_support_access(id) ON DELETE RESTRICT,
    DROP CONSTRAINT admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN ('organization', 'membership', 'profile', 'bulk_job',
                               'session', 'device', 'stream', 'support_access'));

CREATE INDEX admin_audit_events_support_access_idx
    ON public.admin_audit_events(support_access_id)
    WHERE support_access_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Events of the new target types and events written under support access
-- cannot be represented by the predecessor schema. Remove them before
-- restoring its narrower check so rollback works after the feature has been
-- used. The immutability trigger is dropped for the delete and re-added last.
DROP TRIGGER IF EXISTS admin_audit_events_immutable ON public.admin_audit_events;
DELETE FROM public.admin_audit_events
WHERE target_type IN ('session', 'device', 'stream', 'support_access')
   OR support_access_id IS NOT NULL;

DROP INDEX IF EXISTS public.admin_audit_events_support_access_idx;
ALTER TABLE public.admin_audit_events
    DROP COLUMN support_access_id,
    DROP CONSTRAINT admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN ('organization', 'membership', 'profile', 'bulk_job'));

CREATE TRIGGER admin_audit_events_immutable
BEFORE UPDATE OR DELETE ON public.admin_audit_events
FOR EACH ROW EXECUTE FUNCTION public.reject_admin_audit_event_mutation();
-- +goose StatementEnd
