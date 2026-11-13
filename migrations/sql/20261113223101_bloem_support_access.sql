-- +goose Up
-- +goose StatementBegin
-- Break-glass support access: a platform administrator who is not a member of
-- a customer organization opens it with a stated reason. One row per opening.
-- The row is the record the organization's administrators see in Activity, so
-- it is append-only: a row is closed, never edited or deleted.
CREATE TABLE public.bloem_support_access (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE RESTRICT,
    actor_account_id integer NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    -- The login session the access was opened from. The access lives no longer
    -- than that session.
    login_session_id text NOT NULL CHECK (login_session_id <> ''),
    reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 500),
    opened_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    closed_at timestamptz,
    CHECK (expires_at > opened_at AND expires_at <= opened_at + interval '4 hours')
);

CREATE INDEX bloem_support_access_organization_idx
    ON public.bloem_support_access(organization_id, opened_at DESC);
CREATE INDEX bloem_support_access_actor_idx
    ON public.bloem_support_access(actor_account_id, opened_at DESC);

CREATE FUNCTION public.bloem_support_access_append_only()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.closed_at IS NULL
       AND NEW.closed_at IS NOT NULL
       AND (to_jsonb(NEW) - 'closed_at') = (to_jsonb(OLD) - 'closed_at') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'support access records are append-only';
END;
$$;

CREATE TRIGGER bloem_support_access_append_only
BEFORE UPDATE OR DELETE ON public.bloem_support_access
FOR EACH ROW EXECUTE FUNCTION public.bloem_support_access_append_only();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS bloem_support_access_append_only ON public.bloem_support_access;
DROP FUNCTION IF EXISTS public.bloem_support_access_append_only();
DROP TABLE IF EXISTS public.bloem_support_access;
-- +goose StatementEnd
