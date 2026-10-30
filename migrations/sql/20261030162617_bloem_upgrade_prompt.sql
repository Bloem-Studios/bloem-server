-- +goose Up
-- Upgrade prompt: when a viewer is refused a stream because their plan's
-- simultaneous-stream limit is reached, the server can drop an alert with a
-- button into their inbox. The storefront sets the wording and the link; one
-- row holds it for the whole installation.
CREATE TABLE public.bloem_upgrade_prompt (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    enabled boolean NOT NULL DEFAULT false,
    title text NOT NULL DEFAULT '' CHECK (length(title) <= 80),
    body text NOT NULL DEFAULT '' CHECK (length(body) <= 240),
    cta_label text NOT NULL DEFAULT '' CHECK (length(cta_label) <= 40),
    cta_url text NOT NULL DEFAULT '' CHECK (cta_url = '' OR cta_url ~ '^https://'),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (NOT enabled OR (title <> '' AND cta_label <> '' AND cta_url <> ''))
);

-- +goose Down
DROP TABLE public.bloem_upgrade_prompt;
