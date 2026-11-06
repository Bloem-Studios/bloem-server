-- +goose Up
-- +goose StatementBegin
-- auth_sessions.device_id goes back to Silo's shape: NULL when the client
-- reported no device. Bloem stored '' instead (NOT NULL DEFAULT '', and a
-- trigger turning Silo's NULL into ''), but Silo's queries read NULL as "this
-- login names no device": push retention keeps an Apple registration while
-- such a login is live. A blank string never matched, so the sweep removed
-- registrations Silo keeps. The direct-profile binding check already treats
-- NULL as blank (bloem_login_text_blank), and every read coalesces to ''.
DROP TRIGGER IF EXISTS auth_sessions_00_device_id_blank ON public.auth_sessions;
DROP FUNCTION IF EXISTS public.bloem_auth_session_device_id_blank();
ALTER TABLE public.auth_sessions
    ALTER COLUMN device_id DROP DEFAULT,
    ALTER COLUMN device_id DROP NOT NULL;
UPDATE public.auth_sessions SET device_id = NULL WHERE device_id = '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE public.auth_sessions SET device_id = '' WHERE device_id IS NULL;
ALTER TABLE public.auth_sessions
    ALTER COLUMN device_id SET DEFAULT '',
    ALTER COLUMN device_id SET NOT NULL;
CREATE FUNCTION public.bloem_auth_session_device_id_blank()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.device_id := COALESCE(NEW.device_id, '');
    RETURN NEW;
END;
$$;
CREATE TRIGGER auth_sessions_00_device_id_blank
BEFORE INSERT OR UPDATE OF device_id ON public.auth_sessions
FOR EACH ROW EXECUTE FUNCTION public.bloem_auth_session_device_id_blank();
-- +goose StatementEnd
