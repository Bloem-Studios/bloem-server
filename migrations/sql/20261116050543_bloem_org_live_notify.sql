-- +goose Up
-- +goose StatementBegin
-- Change signals for the organization pages' live updates
-- (docs/design/bloem-org-live.md). A trigger on each table an organization page
-- lists sends one small NOTIFY on the bloem_org_live channel: the channel name,
-- the organization, an item id, a revision and a kind. A NOTIFY inside a
-- transaction is delivered when it commits, so a rolled-back change never
-- signals, and every writer is covered, including the bulk-job worker and
-- other nodes. The payload names no person, email or content: the client
-- re-reads through the REST routes, which hold the authorization.
--
-- Only tables Bloem owns carry a trigger. Playback sessions, which Silo owns,
-- are signalled from the server's own lifecycle events instead.
--
-- A signal must never fail the write that caused it, so each function swallows
-- its own errors.
CREATE FUNCTION public.bloem_org_live_notify(channel text, organization uuid, item_id text, revision text, kind text)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    IF organization IS NULL OR item_id IS NULL OR item_id = '' THEN
        RETURN;
    END IF;
    PERFORM pg_notify('bloem_org_live', json_build_object(
        'ch', channel,
        'org', organization,
        'id', item_id,
        'rev', COALESCE(revision, ''),
        'kind', kind
    )::text);
EXCEPTION WHEN OTHERS THEN
    NULL;
END;
$$;

-- People: a membership appearing, changing or going away.
CREATE FUNCTION public.bloem_org_live_membership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM public.bloem_org_live_notify('org.people', OLD.organization_id, OLD.account_id::text,
            'deleted:' || OLD.security_revision::text, 'deleted');
        RETURN OLD;
    END IF;
    PERFORM public.bloem_org_live_notify('org.people', NEW.organization_id, NEW.account_id::text,
        NEW.status || ':' || NEW.legacy_role || ':' || NEW.security_revision::text || ':'
            || to_char(NEW.updated_at AT TIME ZONE 'UTC', 'YYYYMMDDHH24MISSUS'),
        CASE WHEN TG_OP = 'INSERT' THEN 'created' ELSE 'updated' END);
    RETURN NEW;
EXCEPTION WHEN OTHERS THEN
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER organization_memberships_org_live
AFTER INSERT OR UPDATE OR DELETE ON public.organization_memberships
FOR EACH ROW EXECUTE FUNCTION public.bloem_org_live_membership();

-- Activity: a new entry in the organization's activity list. Entries are
-- immutable, so only INSERT matters. An entry about a person also marks that
-- person changed (a profile edit, a group move), so the people list follows
-- administrative actions the membership row does not show.
CREATE FUNCTION public.bloem_org_live_admin_audit()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM public.bloem_org_live_notify('org.activity', NEW.organization_id,
        'lifecycle:' || NEW.id::text, 'lifecycle:' || NEW.id::text, 'created');
    IF NEW.subject_id ~ '^[0-9]{1,18}$' THEN
        PERFORM public.bloem_org_live_notify('org.people', NEW.organization_id,
            NEW.subject_id, 'audit:' || NEW.id::text, 'updated');
    END IF;
    RETURN NEW;
EXCEPTION WHEN OTHERS THEN
    RETURN NEW;
END;
$$;

CREATE TRIGGER admin_audit_events_org_live
AFTER INSERT ON public.admin_audit_events
FOR EACH ROW EXECUTE FUNCTION public.bloem_org_live_admin_audit();

CREATE FUNCTION public.bloem_org_live_entitlement_audit()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM public.bloem_org_live_notify('org.activity', NEW.organization_id,
        'entitlement:' || NEW.id::text, 'entitlement:' || NEW.id::text, 'created');
    IF NEW.target_account_id IS NOT NULL THEN
        PERFORM public.bloem_org_live_notify('org.people', NEW.organization_id,
            NEW.target_account_id::text, 'entitlement:' || NEW.id::text, 'updated');
    END IF;
    RETURN NEW;
EXCEPTION WHEN OTHERS THEN
    RETURN NEW;
END;
$$;

CREATE TRIGGER entitlement_audit_events_org_live
AFTER INSERT ON public.entitlement_audit_events
FOR EACH ROW EXECUTE FUNCTION public.bloem_org_live_entitlement_audit();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS entitlement_audit_events_org_live ON public.entitlement_audit_events;
DROP FUNCTION IF EXISTS public.bloem_org_live_entitlement_audit();
DROP TRIGGER IF EXISTS admin_audit_events_org_live ON public.admin_audit_events;
DROP FUNCTION IF EXISTS public.bloem_org_live_admin_audit();
DROP TRIGGER IF EXISTS organization_memberships_org_live ON public.organization_memberships;
DROP FUNCTION IF EXISTS public.bloem_org_live_membership();
DROP FUNCTION IF EXISTS public.bloem_org_live_notify(text, uuid, text, text, text);
-- +goose StatementEnd
