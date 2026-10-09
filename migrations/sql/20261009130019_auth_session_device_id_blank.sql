-- +goose Up
-- +goose StatementBegin
-- Upstream Silo writes NULLIF(device, '') into auth_sessions.device_id, which is
-- NULL when a client sends no device headers. Bloem keeps the column NOT NULL
-- DEFAULT '' for direct-profile binding, so a Silo login on a shared database
-- failed. NULL and '' mean the same thing here: no device reported.
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

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER auth_sessions_00_device_id_blank ON public.auth_sessions;
DROP FUNCTION public.bloem_auth_session_device_id_blank();
-- +goose StatementEnd
