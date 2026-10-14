-- +goose Up
CREATE TABLE public.bloem_preview_personal_presentation (
    account_id integer PRIMARY KEY REFERENCES public.users(id) ON DELETE CASCADE,
    ends_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE public.bloem_preview_personal_presentation IS 'Personal preview display deadlines. Does not control account expiration or access.';

-- +goose Down
DROP TABLE public.bloem_preview_personal_presentation;
