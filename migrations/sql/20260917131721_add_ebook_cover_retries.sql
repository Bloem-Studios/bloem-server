-- +goose Up
-- +goose StatementBegin
-- An ebook file whose local cover upload failed is listed here until a later
-- scan uploads it. The scanner used to record this by writing the file's
-- group_key_version one behind, which also hid the file from sibling-format
-- grouping lookups; retry state is now kept apart from grouping identity.
CREATE TABLE IF NOT EXISTS ebook_cover_retries (
    media_file_id BIGINT PRIMARY KEY REFERENCES media_files(id) ON DELETE CASCADE,
    failed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ebook_cover_retries;
-- +goose StatementEnd
