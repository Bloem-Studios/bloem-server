-- +goose Up
-- The change token a storage source returned for a directory's last complete
-- listing. The next scan of that directory lists only what changed since.
CREATE TABLE public.bloem_storage_change_tokens (
    source_key uuid NOT NULL REFERENCES public.bloem_storage_sources(key) ON DELETE CASCADE,
    directory_id text NOT NULL CHECK (octet_length(directory_id) BETWEEN 1 AND 1024),
    token text NOT NULL CHECK (octet_length(token) BETWEEN 1 AND 4096),
    configuration_revision bigint NOT NULL CHECK (configuration_revision > 0),
    issued_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_key, directory_id)
);

-- +goose Down
DROP TABLE public.bloem_storage_change_tokens;
