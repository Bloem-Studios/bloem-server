-- +goose Up
ALTER TABLE public.ambience_packs ADD COLUMN repeat_yearly boolean NOT NULL DEFAULT false;
ALTER TABLE public.ambience_packs ADD COLUMN timezone text NOT NULL DEFAULT 'UTC';

-- +goose Down
ALTER TABLE public.ambience_packs DROP COLUMN timezone;
ALTER TABLE public.ambience_packs DROP COLUMN repeat_yearly;
