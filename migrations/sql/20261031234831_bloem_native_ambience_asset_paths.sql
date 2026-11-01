-- Uploaded seasonal and campaign artwork moved from /api/v1/ambience/assets/ to
-- Bloem's native /api/bloem/v1/ambience/assets/ (ambience.AssetURLBase), which
-- serves the same files. Silo will retire /api/v1; rewriting the paths stored in
-- Bloem's own tables keeps that artwork reachable afterwards. Only Bloem tables
-- change: seasonal pack assets, campaign images and playback-card videos.
-- Both prefixes stay valid input, so Down can restore the old paths.

-- +goose Up
UPDATE public.ambience_packs
SET assets = replace(assets::text, '"/api/v1/ambience/assets/', '"/api/bloem/v1/ambience/assets/')::jsonb
WHERE assets::text LIKE '%"/api/v1/ambience/assets/%';

UPDATE public.promotions
SET image_url = '/api/bloem/v1/ambience/assets/' || substr(image_url, length('/api/v1/ambience/assets/') + 1)
WHERE image_url LIKE '/api/v1/ambience/assets/%';

UPDATE public.promotions
SET placement = replace(placement::text, '"/api/v1/ambience/assets/', '"/api/bloem/v1/ambience/assets/')::jsonb
WHERE placement::text LIKE '%"/api/v1/ambience/assets/%';

-- +goose Down
UPDATE public.ambience_packs
SET assets = replace(assets::text, '"/api/bloem/v1/ambience/assets/', '"/api/v1/ambience/assets/')::jsonb
WHERE assets::text LIKE '%"/api/bloem/v1/ambience/assets/%';

UPDATE public.promotions
SET image_url = '/api/v1/ambience/assets/' || substr(image_url, length('/api/bloem/v1/ambience/assets/') + 1)
WHERE image_url LIKE '/api/bloem/v1/ambience/assets/%';

UPDATE public.promotions
SET placement = replace(placement::text, '"/api/bloem/v1/ambience/assets/', '"/api/v1/ambience/assets/')::jsonb
WHERE placement::text LIKE '%"/api/bloem/v1/ambience/assets/%';
