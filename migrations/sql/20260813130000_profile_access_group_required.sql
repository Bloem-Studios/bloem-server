-- +goose Up
-- +goose StatementBegin
-- Every organization needs a canonical fallback before profile assignments can
-- become mandatory. Prefer an existing group so upgrades do not invent policy
-- when an administrator has already configured one.
WITH missing_defaults AS (
    SELECT organizations.id AS organization_id
    FROM public.organizations
    WHERE NOT EXISTS (
        SELECT 1
        FROM public.access_groups
        WHERE access_groups.organization_id = organizations.id
          AND access_groups.is_default
    )
), candidates AS (
    SELECT DISTINCT ON (groups.organization_id)
        groups.organization_id,
        groups.id
    FROM public.access_groups AS groups
    JOIN missing_defaults
      ON missing_defaults.organization_id = groups.organization_id
    ORDER BY groups.organization_id, groups.id
)
UPDATE public.access_groups AS groups
SET is_default = true,
    updated_at = now()
FROM candidates
WHERE groups.organization_id = candidates.organization_id
  AND groups.id = candidates.id;

INSERT INTO public.access_groups (
    organization_id,
    name,
    description,
    is_default,
    library_ids,
    max_playback_quality,
    download_allowed,
    download_transcode_allowed,
    max_streams,
    max_transcodes,
    allowed_permissions,
    requests_allowed
)
SELECT
    organizations.id,
    'Default Group',
    'Applied automatically to newly created users.',
    true,
    NULL,
    '',
    true,
    false,
    5,
    5,
    ARRAY['marker_edit'],
    true
FROM public.organizations
WHERE NOT EXISTS (
    SELECT 1
    FROM public.access_groups
    WHERE access_groups.organization_id = organizations.id
);

UPDATE public.user_profiles AS profiles
SET access_group_id = groups.id,
    updated_at = now()
FROM public.access_groups AS groups
WHERE profiles.organization_id = groups.organization_id
  AND groups.is_default
  AND profiles.access_group_id IS NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.user_profiles
        WHERE access_group_id IS NULL
    ) THEN
        RAISE EXCEPTION 'profile access-group migration left an unassigned profile';
    END IF;
END;
$$;

ALTER TABLE public.user_profiles
    ALTER COLUMN access_group_id SET NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.user_profiles
    ALTER COLUMN access_group_id DROP NOT NULL;
-- Backfilled assignments and promoted/created defaults are intentionally kept:
-- removing them would broaden access through legacy account-only fallback.
-- +goose StatementEnd
