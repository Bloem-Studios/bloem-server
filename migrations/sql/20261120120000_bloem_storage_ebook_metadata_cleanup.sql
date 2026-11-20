-- +goose Up
-- Some storage books' files pack every subject into one entry separated by
-- semicolons, BISAC codes among them ("Historical Romance;Novels;FIC042030"),
-- and a few carry an impossible publication date (2922 for 2022). Ingest now
-- splits the subjects, drops the codes and ignores a date more than a year
-- ahead (normalizeStorageEbook in internal/scanner/storage_ebook.go). A scan
-- merges genres into the stored ones and keeps a stored year, so the books
-- stored before that are cleaned here the same way, except where an operator
-- locked the field (locked_fields 2 = genres, 13 = release dates), and are
-- queued for the search index.

WITH storage_books AS (
    SELECT m.content_id, m.genres
    FROM public.media_items m
    WHERE m.type = 'ebook'
      AND NOT (2 = ANY (COALESCE(m.locked_fields, '{}')))
      AND EXISTS (SELECT 1 FROM unnest(m.genres) AS g(value)
                  WHERE g.value LIKE '%;%' OR btrim(g.value) ~ '^[A-Z]{3}[0-9]{6}$')
      AND EXISTS (SELECT 1 FROM public.media_files f
                  WHERE f.content_id = m.content_id AND f.file_path LIKE 'bloem-storage:%')
),
cleaned AS (
    SELECT b.content_id, COALESCE((
        SELECT array_agg(firsts.part ORDER BY firsts.ord)
        FROM (
            SELECT DISTINCT ON (lower(parts.part)) parts.part, parts.ord
            FROM (
                SELECT btrim(p.part) AS part, g.i * 10000 + p.j AS ord
                FROM unnest(b.genres) WITH ORDINALITY AS g(value, i)
                CROSS JOIN LATERAL unnest(string_to_array(g.value, ';')) WITH ORDINALITY AS p(part, j)
            ) parts
            WHERE parts.part <> '' AND parts.part !~ '^[A-Z]{3}[0-9]{6}$'
            ORDER BY lower(parts.part), parts.ord
        ) firsts
    ), '{}'::text[]) AS genres
    FROM storage_books b
),
updated AS (
    UPDATE public.media_items m
    SET genres = c.genres, updated_at = now()
    FROM cleaned c
    WHERE m.content_id = c.content_id AND m.genres IS DISTINCT FROM c.genres
    RETURNING m.content_id
)
INSERT INTO public.catalog_search_index_events (provider, action, content_id, previous_content_id)
SELECT 'meilisearch', 'upsert', updated.content_id, ''
FROM updated
WHERE public.catalog_search_capture_enabled();

WITH updated AS (
    UPDATE public.media_items m
    SET year = 0, release_date = NULL, updated_at = now()
    WHERE m.type = 'ebook'
      AND NOT (13 = ANY (COALESCE(m.locked_fields, '{}')))
      AND (m.year > EXTRACT(YEAR FROM now())::int + 1
           OR EXTRACT(YEAR FROM m.release_date)::int > EXTRACT(YEAR FROM now())::int + 1)
      AND EXISTS (SELECT 1 FROM public.media_files f
                  WHERE f.content_id = m.content_id AND f.file_path LIKE 'bloem-storage:%')
    RETURNING m.content_id
)
INSERT INTO public.catalog_search_index_events (provider, action, content_id, previous_content_id)
SELECT 'meilisearch', 'upsert', updated.content_id, ''
FROM updated
WHERE public.catalog_search_capture_enabled();

-- +goose Down
-- The cleaned values are not restored: the joined strings and the impossible
-- dates were never correct.
SELECT 1;
