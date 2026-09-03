-- +goose Up
-- playback.proxy_policy was this fork's coarse answer to "may a pooled proxy
-- serve these bytes": one enum covering every delivery at once. Upstream's
-- per-delivery routing settings express the same intent precisely, so the old
-- key is removed here rather than left as a second, silently ignored authority.
--
-- The intent worth preserving is the reason the setting existed: a remote
-- transcode node that does not share this server's media storage must never be
-- handed direct play or remux. Defaulting those deployments back to
-- prefer_proxy would reintroduce exactly the misconfiguration the setting was
-- added to prevent, so both non-default values are translated before removal.
--
-- 'always' needs no rows: it is the historical default and matches the
-- prefer_proxy defaults the new keys already carry.

-- 'never': no delivery may egress through a proxy.
INSERT INTO server_settings (key, value)
SELECT unnest(ARRAY[
        'playback.routing.direct_play_egress',
        'playback.routing.remux_egress',
        'playback.routing.video_transcode_egress'
    ]), 'api_only'
WHERE EXISTS (
    SELECT 1
    FROM server_settings
    WHERE key = 'playback.proxy_policy'
      AND lower(trim(value)) = 'never'
)
ON CONFLICT (key) DO NOTHING;

-- 'transcode_only': direct play and remux stay on the API server; transcoded
-- output keeps its proxy egress, which is the prefer_proxy default.
INSERT INTO server_settings (key, value)
SELECT unnest(ARRAY[
        'playback.routing.direct_play_egress',
        'playback.routing.remux_egress'
    ]), 'api_only'
WHERE EXISTS (
    SELECT 1
    FROM server_settings
    WHERE key = 'playback.proxy_policy'
      AND lower(trim(value)) = 'transcode_only'
)
ON CONFLICT (key) DO NOTHING;

DELETE FROM server_settings WHERE key = 'playback.proxy_policy';

-- +goose Down
-- Recover the closest enum from the egress settings. api_only on direct play
-- means the proxy was kept off the identity deliveries; whether transcode also
-- refused it separates 'never' from 'transcode_only'.
INSERT INTO server_settings (key, value)
SELECT 'playback.proxy_policy',
    CASE
        WHEN EXISTS (
            SELECT 1 FROM server_settings
            WHERE key = 'playback.routing.video_transcode_egress'
              AND lower(trim(value)) = 'api_only'
        ) THEN 'never'
        ELSE 'transcode_only'
    END
WHERE EXISTS (
    SELECT 1
    FROM server_settings
    WHERE key = 'playback.routing.direct_play_egress'
      AND lower(trim(value)) = 'api_only'
)
ON CONFLICT (key) DO NOTHING;
