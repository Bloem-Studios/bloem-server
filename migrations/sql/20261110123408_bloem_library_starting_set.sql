-- +goose Up
-- The starting set is the creation-default entitlement bundle: the libraries
-- and plugins every new organization is granted. A platform library now joins
-- the set's active revision when the transaction that created it commits, and
-- leaves every revision when it is deleted.
--
-- The join runs at commit so that a library the same transaction hands to an
-- organization (nativestorage AttachCreationTx changes owner_id after Silo's
-- INSERT) stays out: only a library still owned by the platform joins.

ALTER TABLE public.entitlement_bundle_members
    DROP CONSTRAINT entitlement_bundle_members_media_folder_fkey,
    ADD CONSTRAINT entitlement_bundle_members_media_folder_fkey
        FOREIGN KEY (media_folder_id, root_owner_id)
        REFERENCES public.media_folders(id, owner_id) ON DELETE CASCADE;

-- +goose StatementBegin
CREATE FUNCTION public.bloem_add_media_folder_to_starting_set()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO public.entitlement_bundle_members (
        bundle_id, bundle_revision, entitlement_kind, root_kind,
        root_owner_id, root_owner_kind, media_folder_id
    )
    SELECT bundles.id, bundles.active_revision, 'library_access', 'media_folder',
           folders.owner_id, 'platform', folders.id
    FROM public.media_folders AS folders
    JOIN public.resource_owners AS owners
      ON owners.id = folders.owner_id AND owners.kind = 'platform'
    CROSS JOIN public.entitlement_bundles AS bundles
    WHERE folders.id = NEW.id
      AND bundles.is_organization_creation_default
    ON CONFLICT DO NOTHING;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER media_folders_join_starting_set
AFTER INSERT ON public.media_folders
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.bloem_add_media_folder_to_starting_set();

-- Platform libraries created before this migration join too.
INSERT INTO public.entitlement_bundle_members (
    bundle_id, bundle_revision, entitlement_kind, root_kind,
    root_owner_id, root_owner_kind, media_folder_id
)
SELECT bundles.id, bundles.active_revision, 'library_access', 'media_folder',
       folders.owner_id, 'platform', folders.id
FROM public.media_folders AS folders
JOIN public.resource_owners AS owners
  ON owners.id = folders.owner_id AND owners.kind = 'platform'
CROSS JOIN public.entitlement_bundles AS bundles
WHERE bundles.is_organization_creation_default
ON CONFLICT DO NOTHING;

-- +goose Down
-- Libraries the Up step added stay in the starting set.
DROP TRIGGER media_folders_join_starting_set ON public.media_folders;
DROP FUNCTION public.bloem_add_media_folder_to_starting_set();
ALTER TABLE public.entitlement_bundle_members
    DROP CONSTRAINT entitlement_bundle_members_media_folder_fkey,
    ADD CONSTRAINT entitlement_bundle_members_media_folder_fkey
        FOREIGN KEY (media_folder_id, root_owner_id)
        REFERENCES public.media_folders(id, owner_id) ON DELETE RESTRICT;
