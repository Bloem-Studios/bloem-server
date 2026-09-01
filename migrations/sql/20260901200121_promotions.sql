-- +goose Up

-- S-2 promotions (docs/specs/client-engagement.md section B, amendments 3+4).
-- One row per campaign card: the surfaces it may appear on, free-form
-- placement hints, the 16:9 card copy + artwork, an optional deeplink / CTA,
-- a UTC window, S-1 targeting, and a priority for ordering. organization_id
-- NULL means deployment-wide. created_by has no FK: accounts may be deleted
-- while the row lives on. There are deliberately no timer / forced-wait
-- columns: the client always keeps "continue to content" as the default action.
CREATE TABLE public.promotions (
    id text PRIMARY KEY,
    organization_id uuid REFERENCES public.organizations(id) ON DELETE CASCADE,
    surfaces text[] NOT NULL,
    placement jsonb NOT NULL DEFAULT '{}'::jsonb,
    kicker text NOT NULL DEFAULT '',
    headline text NOT NULL,
    subtitle text NOT NULL DEFAULT '',
    image_url text NOT NULL,
    image_width integer,
    image_height integer,
    deeplink text NOT NULL DEFAULT '',
    cta jsonb,
    priority integer NOT NULL DEFAULT 0,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    targeting jsonb NOT NULL DEFAULT '{"audience":"all"}'::jsonb,
    dismissible boolean NOT NULL DEFAULT true,
    created_by integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT promotions_window_check CHECK (starts_at < ends_at),
    CONSTRAINT promotions_surfaces_check CHECK (cardinality(surfaces) > 0)
);

CREATE INDEX promotions_window_idx
    ON public.promotions (starts_at, ends_at);
CREATE INDEX promotions_organization_idx
    ON public.promotions (organization_id)
    WHERE organization_id IS NOT NULL;

-- Per-item promo dismissals ride on the existing per-profile home dismissal
-- store under the surfaces promo:home | promo:detail | promo:pre_playback.
ALTER TABLE public.user_home_item_dismissals
    DROP CONSTRAINT user_home_item_dismissals_surface_check;
ALTER TABLE public.user_home_item_dismissals
    ADD CONSTRAINT user_home_item_dismissals_surface_check
    CHECK (surface IN ('continue_watching', 'next_up', 'promo:home', 'promo:detail', 'promo:pre_playback'));

-- +goose Down

DELETE FROM public.user_home_item_dismissals WHERE surface LIKE 'promo:%';
ALTER TABLE public.user_home_item_dismissals
    DROP CONSTRAINT user_home_item_dismissals_surface_check;
ALTER TABLE public.user_home_item_dismissals
    ADD CONSTRAINT user_home_item_dismissals_surface_check
    CHECK (surface IN ('continue_watching', 'next_up'));

DROP INDEX IF EXISTS public.promotions_organization_idx;
DROP INDEX IF EXISTS public.promotions_window_idx;
DROP TABLE IF EXISTS public.promotions;
