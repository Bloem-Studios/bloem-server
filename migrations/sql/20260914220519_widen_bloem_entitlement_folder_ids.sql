-- +goose Up
-- 20260912231715 widened media_folders.id to bigint and deliberately left the
-- integer children alone, on the stated grounds that the workflows writing them
-- still require int4 IDs. That reasoning holds for every child upstream has,
-- because each is written by a workflow that chose the id it wrote.
--
-- Bloem's entitlement tables are not like that. bloem_entitle_default_
-- organization_media_folder() is a trigger on media_folders: it copies NEW.id
-- into organization_entitlements.media_folder_id on every insert, with nobody
-- choosing anything. An integer child written automatically from a bigint
-- parent does not "retain its existing limits" -- it revokes the parent's.
-- Creating a library with an id above 2^31 fails outright, inside a trigger,
-- with "integer out of range" naming a table the caller never mentioned.
--
-- TestCoreBigintIDsAcrossRepositories has been red on this since the trigger
-- landed. It passes upstream, where no trigger copies the id.
--
-- entitlement_bundle_members.media_folder_id is widened with it. Nothing copies
-- into it automatically today, but it names the same parent for the same
-- purpose, and leaving one half of a pair narrow is how the next one of these
-- gets missed.
--
-- Both tables are small (entitlement grants, not catalog rows), so the rewrite
-- is cheap and needs no maintenance window of its own.
-- +goose StatementBegin
ALTER TABLE public.organization_entitlements ALTER COLUMN media_folder_id TYPE bigint;
ALTER TABLE public.entitlement_bundle_members ALTER COLUMN media_folder_id TYPE bigint;
-- +goose StatementEnd

-- +goose Down
-- Lock before inspecting rows so a concurrent writer cannot pass the guard,
-- and refuse rather than truncate an id that no longer fits. A refusal rolls
-- back the whole migration, including these locks.
-- +goose StatementBegin
LOCK TABLE public.organization_entitlements, public.entitlement_bundle_members IN ACCESS EXCLUSIVE MODE;
DO $$
DECLARE
    target text;
    min_id bigint;
    max_id bigint;
BEGIN
    FOREACH target IN ARRAY ARRAY['organization_entitlements', 'entitlement_bundle_members'] LOOP
        EXECUTE format('SELECT min(media_folder_id), max(media_folder_id) FROM public.%I', target)
            INTO min_id, max_id;
        IF min_id < -2147483648 OR max_id > 2147483647 THEN
            RAISE EXCEPTION 'widen_bloem_entitlement_folder_ids down: public.%.media_folder_id range [%, %] does not fit integer',
                target, min_id, max_id;
        END IF;
    END LOOP;
END
$$;
ALTER TABLE public.entitlement_bundle_members ALTER COLUMN media_folder_id TYPE integer;
ALTER TABLE public.organization_entitlements ALTER COLUMN media_folder_id TYPE integer;
-- +goose StatementEnd
