# jellycompat

A real, in-process implementation of the Jellyfin/Emby protocol, letting
third-party Jellyfin/Emby-compatible apps browse and stream from a Bloem
server. Verified working against real clients: **VidHub** and **Findroid**
(named directly in quirk-handling code and tests, e.g.
`audio_profile_streams_test.go`, `audiobook_exclusion_test.go`).

Start at [`server.go`](server.go) (constructs the server, wiring in auth,
catalog, client-IP resolution, config, Live TV, node pool, recommendations,
scan triggers, secrets, subtitles, user store, and watch state/sync) and
[`router.go`](router.go) (ties the routes together).

## Scope

- **Auth**: `auth.go`, `auth_api_key.go`, `login.go`
- **Browsing**: items, collections, genres, people —
  `handlers_items.go`, `handlers_collections.go`, `handlers_genres_test.go`,
  `handlers_persons.go`
- **Playback**: streaming, scrobbling, direct-play content serving, and
  device-profile negotiation — `streams.go`, `playback_sessions.go`,
  `playback_scrobble.go`, `content_direct.go`, `deviceprofile.go`,
  `deviceprofile_conditions.go`
- **Images**: `images.go`, `image_cache.go`, `image_proxy_tags.go`
- **Sessions/realtime**: `handlers_sessions.go`, `handlers_websocket.go`
- **User data**: favorites and watch state — `handlers_userdata.go`,
  `userdata_direct.go`
- **Display preferences**: [`displayprefs/`](displayprefs/)
- **Live TV**: `handlers_livetv.go`
- **Bundled web client**: an installable Jellyfin web UI —
  `web.go`, `web_component.go`

## How it's reached

Served same-origin, in-process, with no separate port by default — dispatched
directly by `internal/compatgateway`'s `LocalHandlers` (see
`internal/compatgateway/proxy.go`) ahead of the network-proxy path that
package also supports for genuinely separate companion applications. An
operator who wants a dedicated listener on a fixed port can still opt into
one via `cfg.JellyfinCompat.Listen` (`JF_PORT` in `.env`) — see
[`docs/operations/compatibility-applications.md`](../../docs/operations/compatibility-applications.md).
