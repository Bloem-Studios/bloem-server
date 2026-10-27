# October 10 upstream integration

> **Historical record (note added October 27, 2026).** The resolutions below are
> true to the October 10 merges. Several no longer describe current code: Bloem
> removed Jellyfin and Audiobookshelf compatibility on October 13 and then ran
> Silo's servers again from October 18 (`2393c41ee`); the v1 and v2 download
> handlers call Silo's own `ServeDirect`, `ResolveDirectFile` and `Capability`
> again, with the profile read from the request context (`0deddcf59`, October 26),
> and the `*ForProfile` wrappers remain only for an explicit profile such as a
> Jellyfin session's; and
> Bloem's bundle-budget checker and allowance were replaced by Silo's report-only
> checker (October 26). The current state is Silo through `3e2d306b2`, recorded on
> the [front page](../../.github/README.md#current-state) and in the
> [integration changelog](CHANGELOG.md).

This source integration preserves Silo ancestry through normal merges. The
earlier merge imported `9d37930c6`; the next integration imported all twelve
commits through `0c7e4190445baa56c2a780c218ef3056b347ae4e`. The follow-up imports
`2b94fb3dc` and `e437e804fcdc9834ec405a2723ce41b35c865907`. It does not authorize
a deployment or certify a live storage backend.

## Conflict resolutions

| File | Upstream behavior retained | Bloem behavior retained |
| --- | --- | --- |
| `cmd/silo/main.go` | Shared download-service callback, marker thumbnail preparation/event delivery, person-refresh claims and rate configuration. | Profile-session revocation and the owned compatibility download adapter. |
| `internal/api/router.go` | Download-service callback and marker image/queue wiring. | Error-returning session revocation and the existing tenant, profile and native-storage composition. |
| `internal/api/handlers/downloads.go` | Abort an interrupted response after media bytes have started. | Explicit verified-profile wrappers for local and proxy downloads. |
| `internal/downloads/service.go` | Original-file quality ceiling, content disposition and resume validators. | Profile policy through the existing owned context adapter; native callers use explicit profile wrappers. |
| `internal/jellycompat/streams.go` | Remove the old direct-file download implementation in favor of the new download service. | Fresh playback admission for retained compatibility sessions. |
| `web/package.json` | Remove the budget-update package command. | The owned bundle-budget checker and existing dependencies. |
| `web/src/App.tsx` | Lazy sign-in and codec-detection imports. | Bloem home, routes, admin context, profile credentials and seasonal composition. |
| `web/src/hooks/queries/admin/stats.ts` | Use the smaller admin-authority module. | The caller-controlled query enable flag. |

The new `internal/jellycompat/download.go` retains upstream behavior. Bloem
binds GET and HEAD to an owned entry point that denies browse-only or
download-disabled access before decoding an item or reading the catalog. Its
download-service adapter resolves the exact compatibility session's account,
profile and current tenant before calling the shared service. The native and
compatibility surfaces share that service's policy and bandwidth limiters.

Upstream's account-only `ServeDirect` and `ResolveDirectFile` signatures stay
available. Bloem's `ServeDirectForProfile` and `ResolveDirectFileForProfile`
bind the verified subject through the existing request-local policy key.
This keeps upstream download tests and callers compatible without granting
account-wide policy to a profile request.

The generated Kotlin and Swift contracts include upstream's
`MarkerThumbnailReadyPayload` realtime shape. Internal `MarkerThumbnail`
extraction state is explicitly excluded from client generation. The owned
bundle checker retains Bloem's allowance policy after upstream removed its
exported budget helper; the lazy launch path uses a smaller allowance.

## Episode spoilers and plugin package refusals

The follow-up retains upstream's per-profile episode spoiler settings and
episode-image provenance. The only textual conflict is the declaration of
`sectionItemResponse` in `internal/api/handlers/sections.go`: Bloem already owns
that declaration in `bloem_sections_ext.go`. The declaration stays in the
companion with Bloem's promotion field. Upstream's `PosterIsEpisodeStill` and
`BackdropIsEpisodeStill` fields are added there as nullable booleans with
`json:"-"`, preserving v1's frozen JSON shape. Upstream's response builders and
v2 wire fields are retained; false provenance remains distinct from unknown.

The generic plugin service, catalog, installer, updater and new package-error
implementation are taken verbatim from `e437e804f`. Catalog identity is carried
through binary and archive requests, invalid packages are refused before
replacement effects, and administrators receive safe permanent or transient
refusal reasons. Bloem's native-storage registry and publication helpers keep
their separate approval, artifact, state-identity and transactional checks.

The automatic merge preserves captured plugin mutation authority, nullable
episode-file handling, section hooks and Bloem's existing test scaffolding.
The revision-17 settings bindings and reachable v2 client DTOs are regenerated
in the server; sibling client repositories are not written by regeneration.

## Verification boundary

Generated settings, client DTOs, API artifacts and their drift gates must
match the merged source. Focused database, HTTP, compatibility, metadata,
thumbnail and web checks cover the affected behavior; the integration handoff
records the exact commands and outcomes. Passing synthetic provider checks
does not establish production S3 immutability. Bloem requires immutable
versions or verified snapshots for S3 playback; checked ETags alone cannot
meet that boundary.
