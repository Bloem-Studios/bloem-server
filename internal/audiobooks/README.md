# Audiobook and podcast support

> **Provenance.** This package is Silo's. Every Go source file here also exists in
> Silo's `main`; Bloem's differences are one declared seam, its own test files and
> this README. The README was added in `35e9e9f7f` (2026-08-18) and last revised on
> 2026-10-27 against Bloem `main` at `e852f92bf`, which contains Silo through
> `3e2d306b2`. "Seam" here means a Silo-owned file Bloem edits, declared in
> [`contracts/seams.txt`](../../contracts/seams.txt) and checked by `make verify-seams`.

## What is here

`internal/audiobooks` is Silo's audiobook and podcast service: scanning support,
metadata enrichment and its retry state (`enrichment.go`, `enrichment_state.go`),
podcast feeds (`podcastfeed/`), smart collections (`smartcoll/`), recommendations
(`recommender.go`), and Silo's Audiobookshelf-compatible server: the REST handlers in
`abs/`, the Socket.IO server in `abssocket/`, and the stores they use
(`abs_*_store.go`, `service.go`). Audiobookshelf apps reach that server on its own
listener (Silo's `cmd/silo/abs_listener.go`), never on the main web/API port:

- the listener is controlled by the `audiobookshelf_compat.enabled` setting, default
  on (**Admin → Settings → Compatibility → Allow Audiobookshelf apps to connect**;
  changing it needs a restart);
- the port is `ABS_PORT`, default `13378`. Silo made it configurable from the
  environment in `bca55f764` (2026-10-26, #3117); the bundled Compose file pins the
  container side and publishes `ABS_PORT` on the host;
- apps sign in as `account#profile`, and a PIN-protected profile appends `#PIN` to
  the password (`abs/login.go`).

## What Bloem changes

- **Organization scope.** `cmd/silo/main.go` wraps the Audiobookshelf server's scope
  resolver in Bloem's `newTenantAwareViewerResolver`, so a session reaches only its
  organization's libraries. That wiring point is the only integration; the package
  code stays Silo's.
- **One seam.** `enrichment_state.go` changes how `RecordOutcome` counts attempts: a
  new `no_match` streak starts at 1, and every other outcome increments the existing
  count, so earlier repair-attempt history is not reset. Bloem's tests for it are
  `bloem_outcome_attempts_test.go` and `bloem_native_audio_enrichment_db_test.go`
  (seam introduced in `5f70bd5d7`, 2026-09-19).
- **History.** Bloem removed both Silo compatibility servers on 2026-10-13
  (`b8ddec9bf`) and restored Silo's versions on 2026-10-18 (`2393c41ee`) so that
  Silo's code merges without deletion seams. Bloem's earlier additions, the companion
  credential enrollment and the same-origin compatibility gateway, stay removed. See
  [protocol compatibility](../../docs/architecture/bloem-compatibility-removal.md).

## Related contracts

- Native audiobook publication and fixed-file direct playback on Bloem's native
  surface: [the audio contract](../../docs/architecture/bloem-native-audio-playback.md).
- Storage-backed scanning and revision-pinned reads stay host-authorized;
  [native storage](../../docs/architecture/bloem-native-storage.md) documents source
  and location identity. Metadata or provider support does not by itself make an
  item playable.
- Before changing the persistence boundary of the Audiobookshelf stores, read
  [protocol compatibility](../../docs/architecture/bloem-compatibility-removal.md)
  and [Silo switching](../../docs/architecture/silo-backend-switching.md), which
  describes moving one database between Silo and Bloem images.

## Verification scope

This README was checked against the source only. It does not record an
Audiobookshelf app sign-in, playback or progress test against a running server.
