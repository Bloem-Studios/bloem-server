# audiobooks

A real, in-process implementation of the Audiobookshelf protocol, letting
Audiobookshelf-compatible apps connect to a Bloem server for audiobook and
podcast playback. Originally a plugin (`silo-plugin-audiobooks`), absorbed
into this first-party package — see [`doc.go`](doc.go).

**Verification status, stated plainly**: this is contract-verified
(`identity_contract_test.go` exercises the real production handler build
against the protocol contract), not confirmed against a real, shipped
Audiobookshelf client app the way the Jellyfin side is against VidHub and
Findroid. Treat it as protocol-correct but not yet client-verified.

Start at [`service.go`](service.go) (the entry point — builds the handler,
`BuildABSHandler`) and [`abs/handler.go`](abs/handler.go) (the HTTP layer
itself).

## Scope

- **Store layer** (top-level `abs_*.go`): bookmarks, collections, playback
  sessions, playlists, progress, RSS feeds, sessions, smart collections.
- **HTTP handlers** ([`abs/`](abs/)): auth (`login.go`, `jwt.go`), items,
  libraries, bookmarks, playlists, collections, smart collections, RSS
  feeds, continue-listening, listening stats, `/me`, file serving, and
  extras.
- **Realtime**: [`abssocket/`](abssocket/) — a Socket.IO-compatible channel
  real Audiobookshelf clients expect.
- **Podcast RSS refresh**: [`podcastfeed/`](podcastfeed/) — `refresher.go`
  + `store.go`.
- **Smart collections**: [`smartcoll/`](smartcoll/) — query evaluation
  (`evaluator.go`, `query.go`).
- Also: `access_resolver.go`, `config.go`, `cred_validator.go`,
  `enrichment.go`, `media_store.go`, `recommender.go`.

## How it's reached

Served same-origin, in-process, with no separate port by default — dispatched
directly by `internal/compatgateway`'s `LocalHandlers` (see
`internal/compatgateway/proxy.go`) ahead of the network-proxy path that
package also supports for genuinely separate companion applications. An
operator who wants a dedicated listener on a fixed port can still opt into
one via `cfg.AudiobookshelfCompat.Listen` (`ABS_PORT` in `.env`) — see
[`docs/operations/compatibility-applications.md`](../../docs/operations/compatibility-applications.md).
