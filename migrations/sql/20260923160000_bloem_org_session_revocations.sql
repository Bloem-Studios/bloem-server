-- Bloem: remember when an organization revoked a member's access, so an
-- account-login session issued before that moment can no longer select the
-- organization's profiles.
--
-- An organization-scoped "revoke all sessions" bumps the membership's
-- security_revision, which stales every token that carries the revision
-- (admin-context, tenant-bound and direct-profile sessions). An account-login
-- session (auth_sessions.profile_id IS NULL) carries no revision and, when the
-- account still belongs to another organization, is deliberately left alive.
-- Such a session chooses its profile per request with X-Profile-Id, and the
-- profile's organization was resolved without asking whether the member had
-- been revoked there. The API viewer-access chain now refuses a profile whose
-- organization revoked this account after the session was created
-- (internal/api/bloem_org_session_revocation.go).
--
-- The row is written by trigger, so every writer of the revocation shape is
-- covered without the API code having to remember it: a security_revision bump
-- that leaves status and legacy_role unchanged. Role and status changes are
-- not revocations and do not record one. Initial ownership activation
-- (tenancy.Store.ActivateInitialOwnershipInTransaction) has the same shape and
-- also records a row; its only effect is that the new owner's account-login
-- sessions created before the activation must log in again to select that
-- organization's profiles. The recorded time is the revoking
-- transaction's start time (now()), so a session created inside that same
-- transaction is not treated as older than the revocation.
--
-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.bloem_org_session_revocations (
    account_id integer NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE CASCADE,
    revoked_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, organization_id)
);

CREATE FUNCTION public.bloem_record_org_session_revocation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO public.bloem_org_session_revocations (account_id, organization_id, revoked_at)
    VALUES (NEW.account_id, NEW.organization_id, now())
    ON CONFLICT (account_id, organization_id)
    DO UPDATE SET revoked_at = GREATEST(public.bloem_org_session_revocations.revoked_at, EXCLUDED.revoked_at);
    RETURN NULL;
END;
$$;

CREATE TRIGGER organization_memberships_record_session_revocation
    AFTER UPDATE OF security_revision ON public.organization_memberships
    FOR EACH ROW
    WHEN (
        NEW.security_revision > OLD.security_revision
        AND NEW.status IS NOT DISTINCT FROM OLD.status
        AND NEW.legacy_role IS NOT DISTINCT FROM OLD.legacy_role
    )
    EXECUTE FUNCTION public.bloem_record_org_session_revocation();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS organization_memberships_record_session_revocation ON public.organization_memberships;
DROP FUNCTION IF EXISTS public.bloem_record_org_session_revocation();
DROP TABLE IF EXISTS public.bloem_org_session_revocations;
-- +goose StatementEnd
