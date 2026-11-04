-- +goose NO TRANSACTION

-- +goose Up
-- Bloem's audiobook requests are fulfilled from the library when an audiobook
-- carries the request's ISBN or ASIN (bookrequests.audiobookLibraryMatchSQL).
-- Identifiers are matched as stored by any provider: the provider name in any
-- case, an ISBN without hyphens or spaces, an ASIN without surrounding spaces,
-- both in upper case. Silo's unique index holds the raw values, so without this
-- one each reconcile pass read every media file to test each item's identifiers.
-- Only audiobook ISBN and ASIN rows are indexed.
CREATE INDEX CONCURRENTLY IF NOT EXISTS bloem_audiobook_identifiers_idx
 ON public.media_item_provider_ids (
  lower(provider),
  upper(CASE WHEN lower(provider) = 'isbn'
             THEN regexp_replace(provider_id, '[-[:space:]]', '', 'g')
             ELSE btrim(provider_id) END)
 ) WHERE item_type = 'audiobook' AND lower(provider) IN ('isbn', 'asin');

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS public.bloem_audiobook_identifiers_idx;
