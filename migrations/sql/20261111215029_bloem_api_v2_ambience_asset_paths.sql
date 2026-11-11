-- Bloem's native API moved from /api/bloem/v1 to /api/bloem/v2 with no alias,
-- so uploaded seasonal and campaign artwork is served only from
-- /api/bloem/v2/ambience/assets/ (ambience.AssetURLBase). This rewrites the
-- paths stored in Bloem's own tables: seasonal pack assets, campaign images and
-- playback-card videos. Paths still on Silo's retired /api/v1 prefix move too.
-- Absolute URLs are left alone. Down restores the /api/bloem/v1 prefix.

-- +goose Up
UPDATE public.ambience_packs
SET assets = replace(replace(assets::text,
        '"/api/bloem/v1/ambience/assets/', '"/api/bloem/v2/ambience/assets/'),
        '"/api/v1/ambience/assets/', '"/api/bloem/v2/ambience/assets/')::jsonb
WHERE assets::text LIKE '%"/api/bloem/v1/ambience/assets/%'
   OR assets::text LIKE '%"/api/v1/ambience/assets/%';

UPDATE public.promotions
SET image_url = '/api/bloem/v2/ambience/assets/' || substr(image_url, length('/api/bloem/v1/ambience/assets/') + 1)
WHERE image_url LIKE '/api/bloem/v1/ambience/assets/%';

UPDATE public.promotions
SET image_url = '/api/bloem/v2/ambience/assets/' || substr(image_url, length('/api/v1/ambience/assets/') + 1)
WHERE image_url LIKE '/api/v1/ambience/assets/%';

UPDATE public.promotions
SET placement = replace(replace(placement::text,
        '"/api/bloem/v1/ambience/assets/', '"/api/bloem/v2/ambience/assets/'),
        '"/api/v1/ambience/assets/', '"/api/bloem/v2/ambience/assets/')::jsonb
WHERE placement::text LIKE '%"/api/bloem/v1/ambience/assets/%'
   OR placement::text LIKE '%"/api/v1/ambience/assets/%';

-- +goose Down
UPDATE public.ambience_packs
SET assets = replace(assets::text, '"/api/bloem/v2/ambience/assets/', '"/api/bloem/v1/ambience/assets/')::jsonb
WHERE assets::text LIKE '%"/api/bloem/v2/ambience/assets/%';

UPDATE public.promotions
SET image_url = '/api/bloem/v1/ambience/assets/' || substr(image_url, length('/api/bloem/v2/ambience/assets/') + 1)
WHERE image_url LIKE '/api/bloem/v2/ambience/assets/%';

UPDATE public.promotions
SET placement = replace(placement::text, '"/api/bloem/v2/ambience/assets/', '"/api/bloem/v1/ambience/assets/')::jsonb
WHERE placement::text LIKE '%"/api/bloem/v2/ambience/assets/%';
