-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.request_settings ADD COLUMN global_requests boolean NOT NULL DEFAULT false;
ALTER TABLE public.media_requests ADD COLUMN organization_id bigint, ADD COLUMN request_scope_id bigint;
UPDATE public.media_requests r SET organization_id=u.organization_id
FROM public.users u WHERE u.id=r.requested_by_user_id;
UPDATE public.media_requests SET request_scope_id=organization_id;
ALTER TABLE public.media_requests
    ALTER COLUMN request_scope_id SET NOT NULL,
    ADD CONSTRAINT media_requests_scope_check CHECK (request_scope_id=0 OR request_scope_id=organization_id),
    ALTER COLUMN organization_id SET NOT NULL,
    DROP CONSTRAINT media_requests_requested_by_user_id_fkey,
    ADD CONSTRAINT media_requests_organization_user_fkey
        FOREIGN KEY (organization_id, requested_by_user_id)
        REFERENCES public.users (organization_id, id) ON DELETE CASCADE;
DROP INDEX public.idx_media_requests_active_tmdb;
CREATE UNIQUE INDEX idx_media_requests_active_tmdb
    ON public.media_requests (request_scope_id, media_type, provider, tmdb_id)
    WHERE outcome='active' AND status<>'completed';
-- Serialize new requests with scope changes using the existing singleton row.
CREATE FUNCTION public.assign_request_organization_scope() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE global_mode boolean;
BEGIN
    SELECT global_requests INTO global_mode FROM public.request_settings WHERE id=true FOR SHARE;
    SELECT organization_id INTO NEW.organization_id FROM public.users WHERE id=NEW.requested_by_user_id;
    NEW.request_scope_id := CASE WHEN COALESCE(global_mode,false) THEN 0 ELSE NEW.organization_id END;
    RETURN NEW;
END $$;
CREATE TRIGGER media_requests_assign_scope BEFORE INSERT ON public.media_requests
    FOR EACH ROW EXECUTE FUNCTION public.assign_request_organization_scope();

CREATE FUNCTION public.update_request_organization_scope() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE public.media_requests SET request_scope_id=CASE WHEN NEW.global_requests THEN 0 ELSE organization_id END
    WHERE request_scope_id IS DISTINCT FROM CASE WHEN NEW.global_requests THEN 0 ELSE organization_id END;
    RETURN NEW;
END $$;
CREATE TRIGGER request_settings_scope AFTER UPDATE OF global_requests ON public.request_settings
    FOR EACH ROW WHEN (OLD.global_requests IS DISTINCT FROM NEW.global_requests)
    EXECUTE FUNCTION public.update_request_organization_scope();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM public.media_requests
        WHERE outcome='active' AND status<>'completed'
        GROUP BY media_type, provider, tmdb_id HAVING count(*)>1
    ) THEN
        RAISE EXCEPTION 'cannot restore global request uniqueness while organizations have overlapping active requests';
    END IF;
END $$;
DROP TRIGGER request_settings_scope ON public.request_settings;
DROP FUNCTION public.update_request_organization_scope();
DROP TRIGGER media_requests_assign_scope ON public.media_requests;
DROP FUNCTION public.assign_request_organization_scope();
ALTER TABLE public.request_settings DROP COLUMN global_requests;
DROP INDEX public.idx_media_requests_active_tmdb;
CREATE UNIQUE INDEX idx_media_requests_active_tmdb
    ON public.media_requests (media_type, provider, tmdb_id)
    WHERE outcome='active' AND status<>'completed';
ALTER TABLE public.media_requests
    DROP CONSTRAINT media_requests_organization_user_fkey,
    ADD CONSTRAINT media_requests_requested_by_user_id_fkey
        FOREIGN KEY (requested_by_user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    DROP COLUMN request_scope_id,
    DROP COLUMN organization_id;
-- +goose StatementEnd
