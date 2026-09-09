-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.organizations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    access_revision bigint NOT NULL DEFAULT 1 CHECK (access_revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO public.organizations (slug, name)
VALUES ('default', 'Default Organization'), ('platform', 'Platform');

-- Preserve Silo's single-organization insert paths. Organization-aware callers
-- supply their target explicitly; this default never selects another tenant.
CREATE FUNCTION public.default_organization_id() RETURNS bigint
LANGUAGE sql STABLE AS $$
    SELECT id FROM public.organizations WHERE slug = 'default'
$$;

ALTER TABLE public.users
    ADD COLUMN organization_id bigint REFERENCES public.organizations(id) ON DELETE RESTRICT,
    ADD COLUMN organization_role text NOT NULL DEFAULT 'member'
        CHECK (organization_role IN ('member', 'admin'));

UPDATE public.users
SET organization_id = (
    SELECT id FROM public.organizations
    WHERE slug = CASE WHEN users.role = 'admin' THEN 'platform' ELSE 'default' END
);

ALTER TABLE public.users
    ALTER COLUMN organization_id SET NOT NULL,
    ALTER COLUMN organization_id SET DEFAULT public.default_organization_id();

CREATE INDEX users_organization_id_idx ON public.users (organization_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Refuse to discard real tenant ownership. Restoring a backup is required once
-- organizations beyond the two bootstrap rows have been provisioned.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM public.organizations WHERE slug NOT IN ('default', 'platform')) THEN
        RAISE EXCEPTION 'cannot remove organization ownership while additional organizations exist';
    END IF;
END $$;
ALTER TABLE public.users DROP COLUMN organization_role, DROP COLUMN organization_id;
DROP FUNCTION public.default_organization_id();
DROP TABLE public.organizations;
-- +goose StatementEnd
