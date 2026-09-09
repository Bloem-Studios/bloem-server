-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.media_folders
    ADD COLUMN organization_id bigint REFERENCES public.organizations(id) ON DELETE RESTRICT;
CREATE INDEX media_folders_organization_id_idx ON public.media_folders (organization_id);

CREATE TABLE public.organization_library_grants (
    organization_id bigint NOT NULL REFERENCES public.organizations(id) ON DELETE RESTRICT,
    media_folder_id integer NOT NULL REFERENCES public.media_folders(id) ON DELETE CASCADE,
    PRIMARY KEY (organization_id, media_folder_id)
);
CREATE INDEX organization_library_grants_folder_idx ON public.organization_library_grants (media_folder_id);

-- Existing Silo libraries remain available to the bootstrap organizations.
INSERT INTO public.organization_library_grants (organization_id, media_folder_id)
SELECT o.id, f.id FROM public.organizations o CROSS JOIN public.media_folders f
WHERE o.slug IN ('default', 'platform');

CREATE FUNCTION public.check_organization_library_grant() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE owner_id bigint;
BEGIN
    SELECT organization_id INTO owner_id FROM public.media_folders
    WHERE id=NEW.media_folder_id FOR SHARE;
    IF owner_id IS NOT NULL THEN
        RAISE EXCEPTION 'private libraries cannot be granted' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER organization_library_grant_owner
BEFORE INSERT OR UPDATE ON public.organization_library_grants
FOR EACH ROW EXECUTE FUNCTION public.check_organization_library_grant();

CREATE FUNCTION public.check_library_organization_owner() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.organization_id IS DISTINCT FROM OLD.organization_id THEN
        IF EXISTS (SELECT 1 FROM public.organization_library_grants WHERE media_folder_id=NEW.id) THEN
            RAISE EXCEPTION 'revoke grants before changing library ownership' USING ERRCODE='23514';
        END IF;
        UPDATE public.organizations SET access_revision=access_revision+1, updated_at=now()
        WHERE id IN (OLD.organization_id, NEW.organization_id);
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER library_organization_owner
BEFORE UPDATE OF organization_id ON public.media_folders
FOR EACH ROW EXECUTE FUNCTION public.check_library_organization_owner();

CREATE FUNCTION public.revise_organization_library_grants() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='INSERT' THEN
        UPDATE public.organizations SET access_revision=access_revision+1, updated_at=now() WHERE id=NEW.organization_id;
    ELSIF TG_OP='DELETE' THEN
        UPDATE public.organizations SET access_revision=access_revision+1, updated_at=now() WHERE id=OLD.organization_id;
    ELSE
        UPDATE public.organizations SET access_revision=access_revision+1, updated_at=now() WHERE id IN (OLD.organization_id, NEW.organization_id);
    END IF;
    RETURN NULL;
END $$;
CREATE TRIGGER organization_library_grants_revision
AFTER INSERT OR UPDATE OR DELETE ON public.organization_library_grants
FOR EACH ROW EXECUTE FUNCTION public.revise_organization_library_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM public.media_folders WHERE organization_id IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot remove library ownership while private libraries exist';
    END IF;
END $$;
DROP TRIGGER organization_library_grants_revision ON public.organization_library_grants;
DROP FUNCTION public.revise_organization_library_grants();
DROP TRIGGER library_organization_owner ON public.media_folders;
DROP FUNCTION public.check_library_organization_owner();
DROP TRIGGER organization_library_grant_owner ON public.organization_library_grants;
DROP FUNCTION public.check_organization_library_grant();
DROP TABLE public.organization_library_grants;
ALTER TABLE public.media_folders DROP COLUMN organization_id;
-- +goose StatementEnd
