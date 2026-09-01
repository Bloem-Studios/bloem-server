-- +goose Up

-- S-1 alert notifications (docs/specs/client-engagement.md §A).
--
-- Admin-authored announcements: one row per compose action. Deliveries link
-- back to it so an announcement can be listed and withdrawn as a unit.
-- created_by has no FK: accounts may be deleted while the audit row lives on.
CREATE TABLE public.notification_announcements (
    id text PRIMARY KEY,
    type text NOT NULL,
    body jsonb NOT NULL,
    targeting jsonb NOT NULL,
    created_by integer,
    recipient_count integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    withdrawn_at timestamptz,
    CONSTRAINT notification_announcements_type_check
        CHECK (type IN ('system.alert', 'system.announcement'))
);

CREATE INDEX notification_announcements_created_idx
    ON public.notification_announcements (created_at DESC, id DESC);

-- Structured body for system.alert / system.announcement rows; NULL for the
-- catalog-joined release types. The existing episode-row CHECK is untouched.
-- expires_at duplicates body->>'expires_at' as a real column so the inbox and
-- sync queries can filter expired rows with a plain predicate; the delivery
-- repository is the single writer of both. dismissed_at is distinct from
-- read_at: dismiss hides a banner, read clears the badge.
ALTER TABLE public.notification_deliveries
    ADD COLUMN body jsonb,
    ADD COLUMN expires_at timestamptz,
    ADD COLUMN dismissed_at timestamptz,
    ADD COLUMN announcement_id text
        REFERENCES public.notification_announcements(id) ON DELETE SET NULL;

-- One inbox row per (profile, announcement): re-running a fanout no-ops.
CREATE UNIQUE INDEX notification_deliveries_profile_announcement_key
    ON public.notification_deliveries (profile_id, announcement_id)
    WHERE announcement_id IS NOT NULL;

CREATE INDEX notification_deliveries_announcement_idx
    ON public.notification_deliveries (announcement_id)
    WHERE announcement_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS public.notification_deliveries_announcement_idx;
DROP INDEX IF EXISTS public.notification_deliveries_profile_announcement_key;
ALTER TABLE public.notification_deliveries
    DROP COLUMN IF EXISTS announcement_id,
    DROP COLUMN IF EXISTS dismissed_at,
    DROP COLUMN IF EXISTS expires_at,
    DROP COLUMN IF EXISTS body;
DROP INDEX IF EXISTS public.notification_announcements_created_idx;
DROP TABLE IF EXISTS public.notification_announcements;
