-- +goose Up
ALTER TABLE bloem_book_requests
    DROP CONSTRAINT bloem_book_requests_media_type_check,
    ADD CONSTRAINT bloem_book_requests_media_type_check
        CHECK (media_type IN ('audiobook', 'book')),
    ADD CONSTRAINT bloem_book_requests_ebook_identity_check
        CHECK (media_type <> 'book' OR
            (isbn ~ '^97[89][0-9]{10}$' AND identity_key = 'isbn:' || isbn AND asin = ''));

-- +goose Down
-- Refuse rollback while ebook rows exist rather than deleting request history.
ALTER TABLE bloem_book_requests
    DROP CONSTRAINT bloem_book_requests_ebook_identity_check,
    DROP CONSTRAINT bloem_book_requests_media_type_check,
    ADD CONSTRAINT bloem_book_requests_media_type_check CHECK (media_type = 'audiobook');
