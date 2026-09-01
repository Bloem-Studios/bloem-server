-- +goose Up

-- S-3 ambience seasonal packs (docs/specs/client-engagement.md section C,
-- amendment 1). One registry row per season: an open-set effect_id, a UTC
-- window, an intensity hint, the surfaces it applies to, and optional artwork
-- (banner + sprite URLs). organization_id NULL means deployment-wide; a set
-- value scopes the pack to that organization's members on the authenticated
-- payload. created_by has no FK: accounts may be deleted while the row lives on.
CREATE TABLE public.ambience_packs (
    id text PRIMARY KEY,
    effect_id text NOT NULL,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    intensity double precision NOT NULL DEFAULT 1.0,
    surfaces text[] NOT NULL DEFAULT ARRAY['all']::text[],
    assets jsonb NOT NULL DEFAULT '{}'::jsonb,
    organization_id uuid REFERENCES public.organizations(id) ON DELETE CASCADE,
    created_by integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ambience_packs_window_check CHECK (starts_at < ends_at),
    CONSTRAINT ambience_packs_intensity_check CHECK (intensity >= 0 AND intensity <= 1)
);

-- Active-window lookups scan by window; the org column keeps scoped lookups cheap.
CREATE INDEX ambience_packs_window_idx
    ON public.ambience_packs (starts_at, ends_at);
CREATE INDEX ambience_packs_organization_idx
    ON public.ambience_packs (organization_id)
    WHERE organization_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS public.ambience_packs_organization_idx;
DROP INDEX IF EXISTS public.ambience_packs_window_idx;
DROP TABLE IF EXISTS public.ambience_packs;
