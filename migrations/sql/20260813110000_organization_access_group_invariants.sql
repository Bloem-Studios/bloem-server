-- +goose Up
-- +goose StatementBegin
DROP INDEX IF EXISTS public.access_groups_one_default_idx;

CREATE UNIQUE INDEX access_groups_one_default_per_organization_idx
    ON public.access_groups (organization_id)
    WHERE is_default;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.user_profiles AS profiles
        LEFT JOIN public.access_groups AS groups
          ON groups.organization_id = profiles.organization_id
         AND groups.id = profiles.access_group_id
        WHERE profiles.access_group_id IS NOT NULL
          AND groups.id IS NULL
    ) THEN
        RAISE EXCEPTION 'profile references an access group outside its organization';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.access_groups_one_default_per_organization_idx;

UPDATE public.access_groups AS groups
SET is_default = false
WHERE groups.is_default
  AND NOT EXISTS (
      SELECT 1
      FROM public.organizations AS organizations
      WHERE organizations.id = groups.organization_id
        AND organizations.is_default
  );

CREATE UNIQUE INDEX access_groups_one_default_idx
    ON public.access_groups (is_default)
    WHERE is_default;
-- +goose StatementEnd
