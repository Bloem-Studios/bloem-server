-- Bloem: each organization's own domains and branding.
--
-- A registered host maps a request to its organization for presentation only:
-- the branding it shows and the address its emails link to. It never decides
-- authorization; an account still reaches only its own organization. One
-- domain per organization is primary, the address the organization's links
-- use. Branding values are the organization's own copies of the server-wide
-- branding.* settings; an unset key falls back to the server's value.
-- See docs/architecture/bloem-organization-domains.md.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.bloem_organization_domains (
    host text PRIMARY KEY
        CHECK (host = lower(host)
            AND host ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$'),
    organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE CASCADE,
    is_primary boolean NOT NULL DEFAULT false,
    source text NOT NULL CHECK (source IN ('platform', 'garden')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX bloem_organization_domains_organization
    ON public.bloem_organization_domains (organization_id);

CREATE UNIQUE INDEX bloem_organization_domains_one_primary
    ON public.bloem_organization_domains (organization_id) WHERE is_primary;

CREATE TABLE public.bloem_organization_branding (
    organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE CASCADE,
    key text NOT NULL CHECK (key LIKE 'branding.%'),
    value text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, key)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.bloem_organization_branding;
DROP TABLE IF EXISTS public.bloem_organization_domains;
-- +goose StatementEnd
