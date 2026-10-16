-- +goose Up
-- Operator wording for the preview: the button that stands where play would
-- be on a title page, and the sentence in the subscribe dialog. Empty keeps
-- the client's own wording ("Subscribe to watch", read or listen by type).
ALTER TABLE public.bloem_preview_settings
    ADD COLUMN item_cta_label text NOT NULL DEFAULT '',
    ADD COLUMN cta_message text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE public.bloem_preview_settings
    DROP COLUMN cta_message,
    DROP COLUMN item_cta_label;
