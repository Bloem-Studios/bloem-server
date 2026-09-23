-- Bloem: stamp every media request with the organization it was filed in.
--
-- A request's organization used to be derived from the requester's primary
-- membership (default organization first, then oldest). Administrators were
-- bounded by the tenant their session acts for. For an account holding
-- memberships in several organizations the two rules disagree: a request filed
-- while acting in E landed under the older organization T, so T's
-- administrators could act on it and E's never saw it.
--
-- The service now stamps organization_id from the acting tenant when it
-- creates a request (internal/requests/bloem_repository_tenant.go). Rows
-- inserted without a stamp -- existing rows, and callers outside an HTTP
-- request -- take the same organization the old rule chose, so this migration
-- changes no request's visibility on its own:
--
--   1. the requester's primary active membership (tenancy.PrimaryMembershipSQL)
--   2. otherwise any membership of the requester, in the same order
--   3. otherwise the default organization
--
-- Single-membership accounts resolve to their one organization; everything
-- else is deterministic.
--
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION public.bloem_media_request_fallback_organization(p_account_id integer)
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
    SELECT COALESCE(
        (
            SELECT memberships.organization_id
            FROM public.organization_memberships AS memberships
            JOIN public.organizations AS orgs ON orgs.id = memberships.organization_id
            WHERE memberships.account_id = p_account_id
            ORDER BY (memberships.status = 'active') DESC,
                     orgs.is_default DESC,
                     memberships.created_at ASC,
                     memberships.id ASC
            LIMIT 1
        ),
        public.bloem_default_organization_id()
    )
$$;

ALTER TABLE public.media_requests
    ADD COLUMN organization_id uuid REFERENCES public.organizations(id) ON DELETE CASCADE;

UPDATE public.media_requests
SET organization_id = public.bloem_media_request_fallback_organization(requested_by_user_id)
WHERE organization_id IS NULL;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.media_requests WHERE organization_id IS NULL) THEN
        RAISE EXCEPTION 'media request organization backfill left a request without an organization';
    END IF;
END;
$$;

ALTER TABLE public.media_requests
    ALTER COLUMN organization_id SET NOT NULL;

CREATE FUNCTION public.bloem_media_requests_default_organization()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.organization_id IS NULL THEN
        NEW.organization_id := public.bloem_media_request_fallback_organization(NEW.requested_by_user_id);
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER media_requests_default_organization
    BEFORE INSERT ON public.media_requests
    FOR EACH ROW EXECUTE FUNCTION public.bloem_media_requests_default_organization();

CREATE INDEX media_requests_organization_page_order_idx
    ON public.media_requests (organization_id, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS media_requests_default_organization ON public.media_requests;
DROP FUNCTION IF EXISTS public.bloem_media_requests_default_organization();
DROP INDEX IF EXISTS public.media_requests_organization_page_order_idx;
ALTER TABLE public.media_requests DROP COLUMN IF EXISTS organization_id;
DROP FUNCTION IF EXISTS public.bloem_media_request_fallback_organization(integer);
-- +goose StatementEnd
