-- +goose Up
-- Folder deletion batches run in separate, resumable transactions. Fence only
-- new immutable bundle references; blocking every folder FK can deadlock with
-- an in-flight scanner holding a catalog row that a deletion batch needs.
-- Use one helper for both writers and the worker, retaining bigint folder IDs.
-- +goose StatementBegin
CREATE FUNCTION public.bloem_lock_media_folder_bundle_reference(folder_id bigint)
RETURNS void
LANGUAGE sql VOLATILE STRICT
AS $$
 SELECT pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended(
  'bloem:media-folder-bundle-reference:' || folder_id::text, 0));
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_fence_media_folder_bundle_reference()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
 IF NEW.media_folder_id IS NOT NULL THEN
  PERFORM public.bloem_lock_media_folder_bundle_reference(NEW.media_folder_id);
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER bloem_entitlement_bundle_members_folder_fence
BEFORE INSERT OR UPDATE ON public.entitlement_bundle_members
FOR EACH ROW EXECUTE FUNCTION public.bloem_fence_media_folder_bundle_reference();

-- +goose Down
DROP TRIGGER bloem_entitlement_bundle_members_folder_fence ON public.entitlement_bundle_members;
DROP FUNCTION public.bloem_fence_media_folder_bundle_reference();
DROP FUNCTION public.bloem_lock_media_folder_bundle_reference(bigint);
