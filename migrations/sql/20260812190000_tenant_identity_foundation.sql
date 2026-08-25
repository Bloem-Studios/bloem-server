-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.platform_security (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    owner_account_id integer UNIQUE REFERENCES public.users(id) ON DELETE RESTRICT,
    policy_revision bigint NOT NULL DEFAULT 1 CHECK (policy_revision > 0),
    ownership_resolution_required boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE public.organizations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL,
    name text NOT NULL,
    status text NOT NULL CHECK (status IN ('initializing','active','suspended')),
    owner_account_id integer REFERENCES public.users(id) ON DELETE RESTRICT,
    policy_revision bigint NOT NULL DEFAULT 1 CHECK (policy_revision > 0),
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT organizations_active_owner_check
        CHECK (status <> 'active' OR owner_account_id IS NOT NULL)
);

CREATE UNIQUE INDEX organizations_slug_ci_idx ON public.organizations(lower(slug));
CREATE UNIQUE INDEX organizations_one_default_idx ON public.organizations(is_default) WHERE is_default;

CREATE FUNCTION public.bloem_default_organization_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
    SELECT id FROM public.organizations WHERE is_default
$$;

CREATE TABLE public.organization_memberships (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE CASCADE,
    account_id integer NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('invited','active','suspended')),
    legacy_role text NOT NULL CHECK (legacy_role IN ('admin','user')),
    security_revision bigint NOT NULL DEFAULT 1 CHECK (security_revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, account_id)
);

WITH enabled_admins AS (
    SELECT id
    FROM public.users
    WHERE enabled AND role = 'admin'
), ownership AS (
    SELECT COUNT(*) AS admin_count, MIN(id) AS owner_account_id
    FROM enabled_admins
), default_organization AS (
    INSERT INTO public.organizations (slug, name, status, owner_account_id, is_default)
    SELECT
        'default',
        'Default Organization',
        CASE WHEN admin_count = 1 THEN 'active' ELSE 'initializing' END,
        CASE WHEN admin_count = 1 THEN owner_account_id END,
        true
    FROM ownership
    RETURNING id
)
INSERT INTO public.platform_security (singleton, owner_account_id, ownership_resolution_required)
SELECT
    true,
    CASE WHEN ownership.admin_count = 1 THEN ownership.owner_account_id END,
    ownership.admin_count > 1
FROM ownership;

INSERT INTO public.organization_memberships (organization_id, account_id, status, legacy_role)
SELECT
    organizations.id,
    users.id,
    'active',
    CASE WHEN users.role = 'admin' THEN 'admin' ELSE 'user' END
FROM public.users
CROSS JOIN public.organizations
WHERE organizations.is_default;

ALTER TABLE public.user_profiles
    ADD COLUMN organization_id uuid,
    ADD COLUMN access_group_id bigint;

ALTER TABLE public.access_groups
    ADD COLUMN organization_id uuid DEFAULT public.bloem_default_organization_id();

UPDATE public.access_groups
SET organization_id = (SELECT id FROM public.organizations WHERE is_default);

UPDATE public.user_profiles AS profiles
SET
    organization_id = organizations.id,
    access_group_id = users.access_group_id
FROM public.users
CROSS JOIN public.organizations
WHERE profiles.user_id = users.id
  AND organizations.is_default;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.access_groups WHERE organization_id IS NULL) THEN
        RAISE EXCEPTION 'tenant identity migration left an access group without an organization';
    END IF;
    IF EXISTS (SELECT 1 FROM public.user_profiles WHERE organization_id IS NULL) THEN
        RAISE EXCEPTION 'tenant identity migration left a profile without an organization';
    END IF;
END;
$$;

ALTER TABLE public.access_groups
    ALTER COLUMN organization_id SET NOT NULL;

ALTER TABLE public.user_profiles
    ALTER COLUMN organization_id SET NOT NULL;

ALTER TABLE public.access_groups
    DROP CONSTRAINT access_groups_name_key,
    ADD CONSTRAINT access_groups_organization_name_key UNIQUE (organization_id, name),
    ADD CONSTRAINT access_groups_organization_id_id_key UNIQUE (organization_id, id);

ALTER TABLE public.user_profiles
    ADD CONSTRAINT user_profiles_organization_access_group_fkey
    FOREIGN KEY (organization_id, access_group_id)
    REFERENCES public.access_groups(organization_id, id);

CREATE INDEX organization_memberships_account_id_idx
    ON public.organization_memberships(account_id);
CREATE INDEX user_profiles_organization_id_idx
    ON public.user_profiles(organization_id);
CREATE INDEX user_profiles_organization_access_group_id_idx
    ON public.user_profiles(organization_id, access_group_id);
CREATE INDEX access_groups_organization_id_idx
    ON public.access_groups(organization_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.user_profiles
    DROP CONSTRAINT IF EXISTS user_profiles_organization_access_group_fkey;

DROP INDEX IF EXISTS public.user_profiles_organization_access_group_id_idx;
DROP INDEX IF EXISTS public.user_profiles_organization_id_idx;
DROP INDEX IF EXISTS public.access_groups_organization_id_idx;
DROP INDEX IF EXISTS public.organization_memberships_account_id_idx;

ALTER TABLE public.user_profiles
    DROP COLUMN IF EXISTS access_group_id,
    DROP COLUMN IF EXISTS organization_id;

ALTER TABLE public.access_groups
    DROP CONSTRAINT IF EXISTS access_groups_organization_id_id_key,
    DROP CONSTRAINT IF EXISTS access_groups_organization_name_key,
    ADD CONSTRAINT access_groups_name_key UNIQUE (name),
    DROP COLUMN IF EXISTS organization_id;

DROP FUNCTION IF EXISTS public.bloem_default_organization_id();
DROP TABLE IF EXISTS public.organization_memberships;
DROP TABLE IF EXISTS public.organizations;
DROP TABLE IF EXISTS public.platform_security;
-- +goose StatementEnd
