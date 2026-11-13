-- +goose Up
-- +goose StatementBegin
-- A per-profile revision for lost-update checks on profile edits. The
-- account's security revision cannot serve: raising it signs the member out.
-- The constant default is a catalog-only change, and upstream Silo's inserts
-- keep working because they never name the column.
ALTER TABLE public.user_profiles
    ADD COLUMN revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0);

CREATE FUNCTION public.bloem_bump_user_profile_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- Any column a person can edit counts. The revision itself, the update
    -- time and the counters derived from other columns do not.
    IF (to_jsonb(NEW) - 'updated_at' - 'revision' - 'pin_revision' - 'credential_revision')
       IS DISTINCT FROM
       (to_jsonb(OLD) - 'updated_at' - 'revision' - 'pin_revision' - 'credential_revision') THEN
        NEW.revision := OLD.revision + 1;
    ELSE
        NEW.revision := OLD.revision;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER user_profiles_revision
BEFORE UPDATE ON public.user_profiles
FOR EACH ROW EXECUTE FUNCTION public.bloem_bump_user_profile_revision();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS user_profiles_revision ON public.user_profiles;
DROP FUNCTION IF EXISTS public.bloem_bump_user_profile_revision();
ALTER TABLE public.user_profiles DROP COLUMN revision;
-- +goose StatementEnd
