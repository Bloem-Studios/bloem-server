# October 10 upstream integration

This source integration preserves Silo ancestry through normal merges. The
earlier merge imported `9d37930c6`; this integration imports all twelve commits
through `0c7e4190445baa56c2a780c218ef3056b347ae4e`. It does not authorize a
deployment or certify a live storage backend.

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

## Verification boundary

Generated settings, client DTOs, API artifacts and their drift gates must
match the merged source. Focused database, HTTP, compatibility, metadata,
thumbnail and web checks cover the affected behavior; the integration handoff
records the exact commands and outcomes. Passing synthetic provider checks
does not establish production S3 immutability. Bloem requires immutable
versions or verified snapshots for S3 playback; checked ETags alone cannot
meet that boundary.
