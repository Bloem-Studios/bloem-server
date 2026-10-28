-- +goose Up
-- The preview banner's second button: the operator's subscription, beside the
-- trial the first button offers. Empty keeps the plain exit.
ALTER TABLE public.bloem_preview_settings
    ADD COLUMN subscribe_label text NOT NULL DEFAULT '',
    ADD COLUMN subscribe_url text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE public.bloem_preview_settings
    DROP COLUMN subscribe_url,
    DROP COLUMN subscribe_label;
