# Bloem documentation

Index of Bloem-owned documentation in this tracking fork of Silo Server. A
**tracking fork** keeps Silo's Git history and merges Silo's `main` as it moves
(see [FORK.md](../../FORK.md)). Any Markdown file that also exists in
Silo's `main`, including the root `README.md`, `CONTRIBUTING.md`, `AGENTS.md`
and most of `docs/`, is Silo's and stays as Silo wrote it. Bloem's material
lives in the Bloem-owned files listed here; where Bloem differs from a Silo
document, an **overlay** records the difference. The code is the source of truth.

Every current Bloem page is written the same way, after
[How organizations work](../architecture/bloem-organizations.md): plain language and
pictures first, a made-up example where it helps, a "Words used in the code" table, and
a "For developers" section that holds every technical rule.
[Writing Bloem documentation](documentation-workflow.md) sets out that shape.

The current state of the Silo integration is in
[How Bloem keeps up with Silo](../architecture/bloem-following-silo.md).

## Start here

- [Bloem Server front page](../../.github/README.md) — a short introduction: what Bloem is, what it adds, how to run it and where to read on. GitHub shows it in place of the root `README.md`, which is Silo's.
- [Bloem Server in depth](about.md) — where Bloem comes from, what it adds to Silo, the feature list, running, configuring and building it, and licence.
- [How Bloem forks Silo](../../FORK.md) — the tracking-fork policy, the "Silo wins" rule, seams and overlays, and what Bloem adds.
- [Agent instructions](../../BLOEM_AGENTS.md) — Bloem's overrides of Silo's `AGENTS.md` / `CLAUDE.md`; they take precedence where the two differ.
- [Working on Bloem](../../.github/CONTRIBUTING.md) — the support and contribution stance, repository ownership, AI disclosure and the validation commands; the root `CONTRIBUTING.md` is Silo's.
- [Writing Bloem documentation](documentation-workflow.md) — Silo's manual/reference model, where each kind of Bloem change is documented, regeneration and verification commands.
- [How Bloem CI works](ci.md) — the Bloem CI jobs, the merge bot's CI wait, the switched-off daily upstream sync, the published image's gate, runner placement and tool pins.
- [API catalogue](api-catalogue.md) — generated list of typed `/api/v2` and `/api/bloem/v2` operations and native documentation gaps.
- [API by topic](api/README.md) — every typed `/api/bloem/v2` operation, generated from the OpenAPI contract, one page per topic, with credentials, fields and error codes.

## How Bloem works: start here

The overview pages, one per area, for readers who don't work on the code. Each links the
more detailed pages of its area, and ends with a "For developers" section.

- [How organizations work](../architecture/bloem-organizations.md) — the platform, organizations, accounts and profiles; what someone can watch; a reseller's year.
- [How organization domains and branding work](../architecture/bloem-organization-domains.md) — each organization on its own domains, with its own branding and email links; refusing a sign-in on another organization's domain.
- [How organization support works](../architecture/bloem-organization-support.md) — what an organization's administrators can do for their members (reset links, sign-outs, profiles, streams), who may act on whom, and the operator's break-glass access with a stated reason.
- [How household logins work](../architecture/bloem-household-logins.md) — accounts of their own that belong to a main account: enabled only with it, in its group with their own limits, deleted with it; and moving a person between a profile and a login of their own.
- [How plans, previews and upgrades work](../architecture/bloem-plans.md) — the built-in plans, how a plan combines with an account and its profiles, changing a plan safely, the storefront preview, the upgrade prompt.
- [How Live TV works](../architecture/bloem-live-tv.md) — sources, tuning in, recordings.
- [How storage sources work](../architecture/bloem-storage-sources.md) — libraries read from S3 and Bookwarehouse.
- [How requests work](../architecture/bloem-requests.md) — movie and series, audiobook and ebook requests.
- [How search finds titles by words and by meaning](../architecture/bloem-search-and-embeddings.md) — the two halves of a search, dropping the most common words first, task labels for `gemini-embedding-2`, changing the embedding model.
- [How the source code offer works](../architecture/bloem-source-code-offer.md) — every signed-in person can download the complete source of the version the server runs, as the AGPL asks; how each update ships its archive.
- [How messages and promotions work](../architecture/bloem-messages-and-promotions.md) — announcements, campaign and playback cards, seasonal packs.
- [Admin app push notifications](../architecture/bloem-admin-push.md) — which events push to the admin apps, how a device registers, how the phone reads its text, how the server keeps scan and backup Live Activities current without names reaching the relay, and why a plain Silo server cannot send them.
- [Stream insights](../architecture/bloem-stream-insights.md) — where streams come from and how they are connected, the dashboard's stream globe, map and satellite widgets, who was turned away at a stream or transcode limit, the limit alerts organization admins get, and quiet hours.
- [How push notifications reach each app](../architecture/bloem-push-relays.md) — Silo's apps through Silo's relay and Bloem's apps through Bloem's, each with its own credential; what each relay sees, when the server signs up with each, and how a restore keeps both credentials.
- [How apps connect](../architecture/bloem-apps.md) — the three entrances, signing in, remote control.
- [Switching between Silo and Bloem](../architecture/bloem-silo-switching-explained.md) — one database, two servers taking turns, both install layouts.
- [How Bloem keeps up with Silo](../architecture/bloem-following-silo.md) — the merge bot and each Silo batch, its deploy, "Silo wins", where Bloem keeps its own work.
- [How backups work in Bloem](../architecture/bloem-backup.md) — what a restore brings back of Bloem's data, credentials kept from the live server, what selective restore does not cover yet.

The pictures are drawn by `scripts/bloem/draw-pictures.py`.

## Bloem editions of upstream documents

For these, Bloem's text differs throughout; read the Bloem edition instead of the upstream file.

- [docs/non-goals.md](overlays/non-goals.md) (upstream: [docs/non-goals.md](../non-goals.md)) — Live TV and `.strm` remote-URL items in scope, private addresses refused by default.
- [Installing and running Bloem with Docker](overlays/wiki/deployment/docker.md) — maintained Bloem installation and operations guide, in place of an upstream wiki page.
- [Bloem guides for operators and viewers](overlays/wiki/index.md) — Bloem's guide index, alongside the current [Silo reference index](../README.md).

## Overlays on upstream documents

Each overlay lists, by section, what Bloem adds to or replaces in the upstream document of the
same path. Read the upstream document together with its overlay.

- [DEVELOPMENT.md](overlays/DEVELOPMENT.md) — overlay on [DEVELOPMENT.md](../../DEVELOPMENT.md)
- [docs/admin-api.md](overlays/admin-api.md) — overlay on [docs/admin-api.md](../admin-api.md)
- [docs/api-docs.md](overlays/api-docs.md) — overlay on [docs/api-docs.md](../api-docs.md)
- [docs/architecture/api-contract.md](overlays/architecture/api-contract.md) — overlay on [docs/architecture/api-contract.md](../architecture/api-contract.md)
- [docs/architecture/core-id-range.md](overlays/architecture/core-id-range.md) — overlay on [docs/architecture/core-id-range.md](../architecture/core-id-range.md)
- [docs/architecture/history-import-execution.md](overlays/architecture/history-import-execution.md) — overlay on [docs/architecture/history-import-execution.md](../architecture/history-import-execution.md)
- [docs/architecture/invitations-onboarding.md](overlays/architecture/invitations-onboarding.md) — overlay on [docs/architecture/invitations-onboarding.md](../architecture/invitations-onboarding.md)
- [docs/architecture/observability.md](overlays/architecture/observability.md) — overlay on [docs/architecture/observability.md](../architecture/observability.md)
- [docs/architecture/playback-protocol-v3.md](overlays/architecture/playback-protocol-v3.md) — overlay on [docs/architecture/playback-protocol-v3.md](../architecture/playback-protocol-v3.md)
- [docs/architecture/recommendations.md](overlays/architecture/recommendations.md) — overlay on [docs/architecture/recommendations.md](../architecture/recommendations.md)
- [docs/architecture/secret-encryption.md](overlays/architecture/secret-encryption.md) — overlay on [docs/architecture/secret-encryption.md](../architecture/secret-encryption.md)
- [docs/architecture/settings-contract.md](overlays/architecture/settings-contract.md) — overlay on [docs/architecture/settings-contract.md](../architecture/settings-contract.md)
- [docs/architecture/streaming-write-deadline.md](overlays/architecture/streaming-write-deadline.md) — overlay on [docs/architecture/streaming-write-deadline.md](../architecture/streaming-write-deadline.md)
- [docs/architecture/transcode-nodes.md](overlays/architecture/transcode-nodes.md) — overlay on [docs/architecture/transcode-nodes.md](../architecture/transcode-nodes.md)
- [docs/auth-api.md](overlays/auth-api.md) — overlay on [docs/auth-api.md](../auth-api.md)
- [docs/invitations-api.md](overlays/invitations-api.md) — overlay on [docs/invitations-api.md](../invitations-api.md)
- [docs/libraries-api.md](overlays/libraries-api.md) — overlay on [docs/libraries-api.md](../libraries-api.md)
- [docs/realtime-api.md](overlays/realtime-api.md) — overlay on [docs/realtime-api.md](../realtime-api.md)
- [docs/release-versioning.md](overlays/release-versioning.md) — overlay on [docs/release-versioning.md](../release-versioning.md)
- [Bloem S3 storage notes](overlays/s3-storage-setup.md) — maintained additions to the current [blob-storage architecture](../architecture/blob-storage.md).
- [docs/settings-api.md](overlays/settings-api.md) — overlay on [docs/settings-api.md](../settings-api.md)
- [Bloem artwork storage](overlays/wiki/admin/artwork-storage.md) — Bloem campaign/seasonal storage alongside the current [blob-storage architecture](../architecture/blob-storage.md).

## Guides (wiki)

- [Bloem Server admin guide](../wiki/admin-guide.md) — for the person running the server: install, first run, libraries, users and profiles, playback, access policy, Live TV, maintenance.
- [Entitlement templates](../wiki/admin/entitlement-templates.md) — operator guide to revisioned access-policy templates.
- [Bloem user guide](../wiki/user-guide.md) — for viewers: signing in, profiles, finding and playing things, downloads, requests.

## Every page, by area

### Organizations, accounts and policy

- [How organizations work](../architecture/bloem-organizations.md) — start here: the platform, organizations, accounts and profiles in plain language, with six pictures.
- [How multitenant administration works](../architecture/multitenant-administration.md) — the Platform and Organization administrative contexts and what each manages.
- [How library ownership and grants work](../architecture/resource-tenancy-foundation.md) — resource owners and the first resource-isolation boundary.
- [How tenant authorization works](../architecture/opa-tenant-authorization.md) — how tenant facts, OPA and SQL scope catalog and playback reads.
- [How Silo backend switching works](../architecture/silo-backend-switching.md) — Silo and Bloem taking turns on one database: the flag, its invariants, the all-in-one procedure, and `make silo-switch-check` and `make silo-switch-check-all-in-one`.
- [Silo client compatibility check](../../contracts/client/silo-clients/README.md) — `make silo-client-compat`: every call Silo's own apps make, replayed against Bloem and upstream Silo, and the allowlist of known differences, which may only shrink.
- [How request rules apply to organizations and profiles](../architecture/bloem-request-policy.md) — verified profile quotas, scoped administrative usage and group limits on Silo's request service.
- [How retiring an organization and deleting a library stay safe](../architecture/bloem-lifecycle-safety.md) — atomic tenant retirement and library deletion.
- [How Bloem's security foundation works](../architecture/bloem-security-foundation.md) — the `/api/bloem/v2` discovery and administration boundary beside Silo's `/api/v2` login.
- [How Bloem's adapters over Silo work](../architecture/bloem-upstream-adapters.md) — migration filesystem, profile-aware interfaces and embedded-web boundaries over Silo's code.

### Native storage and media delivery

- [How native storage works](../architecture/bloem-native-storage.md) — experimental storage-source (S3, Bookwarehouse) ebook, movie and audiobook libraries: locations, scanning, covers and reading.
- [How stored audiobooks play](../architecture/bloem-native-audio-playback.md) — complete-book publication and pinned direct audio playback.
- [How stored movies play](../architecture/bloem-native-video-playback.md) — source admission, movie publication, execution ownership, worker HLS and supported surfaces.
- [How a trusted tool reads a stored file](../architecture/bloem-native-tool-input.md) — bounded pinned reads for trusted local tools, child lifetime and host admission.
- [How a scan reads a stored file](../architecture/bloem-native-scan-reader.md) — sealed scan pages, pinned entry reads and current scan authority.
- [How Bloem looks inside a stored file](../architecture/bloem-native-probe.md) — trusted ffprobe input, shared read budgets and process cleanup.
- [How playback works across several API servers](../architecture/bloem-cluster-media.md) — which node owns Live TV and native media sessions, on Silo's playback attempts and heartbeats.
- [How access is checked as content leaves the server](../architecture/delivery-boundary-enforcement.md) — library access rechecked where bytes or rows leave the server.

### Requests and preview

- [How audiobook requests work](../architecture/bloem-audiobook-requests.md) — request authority, independent quotas, identifier matching and notification scope.
- [How ebook requests work](../architecture/bloem-ebook-request-foundation.md) — default-off web/admin requests, Bookwarehouse fulfillment, durable recovery and linked-library completion.
- [How browse-only access and the preview work](../architecture/bloem-browse-only-content-policy.md) — protected preview principals, signed guest sessions, catalogue hygiene and native content-delivery boundaries.

### Search and recommendations

- [How search finds titles by words and by meaning](../architecture/bloem-search-and-embeddings.md) — start here: the words and meaning halves of a search, what happens when no title has every word, task labels, changing the embedding model, with two pictures.
- [How the search mirror works](../architecture/bloem-search-mirror.md) — an optional second Meilisearch that Bloem keeps current and swaps roles with after a failover: the four variables, the bookmark, full copies, the swap step, cleanup, failures, metrics and known limits.
- [Recommendations overlay](overlays/architecture/recommendations.md) — what Bloem adds to Silo's recommendations page: task labels for `gemini-embedding-2`.

### Announcements, promotions and seasonal presentation

- [How announcements work](../architecture/admin-announcements.md) — admin-authored messages, targeting and inbox rules.
- [How playback cards work](../architecture/playback-overlays.md) — chapter-triggered campaign cards during playback.
- [How seasonal packs are scheduled](../architecture/seasonal-scheduling.md) — annual ambience schedules evaluated in the selected timezone.
- [Where campaign and seasonal artwork is stored](../architecture/bloem-campaign-asset-storage.md) — campaign and seasonal uploads use the catalog Assets store (local or S3); public S3 is only a fallback.
- [How Bloem picks which campaigns and seasonal packs to show](../architecture/bloem-presentation-evaluators.md) — in-process promotions and ambience evaluation, host authority and section extension boundaries.

### Live TV

- [How `.strm` files play](../architecture/bloem-strm.md) — `.strm` remote-URL items from library folders and storage sources, the SSRF guard and the private-address setting, playback through Bloem.
- [How Xtream Live TV providers work](../architecture/xtream-live-tv.md) — encrypted provider accounts, live channels, XMLTV, connection limits; no VOD or remote URLs.
- [How apps reach Live TV](../architecture/live-tv-client-access.md) — the `watch_live_tv` permission, capability discovery and tuner-session handling.
- [How Live TV finds tuners](../livetv-tuner-discovery.md) — HDHomeRun and Dispatcharr discovery and probing.
- [prairie-source-manifest.tsv](../livetv/prairie-source-manifest.tsv) — every Prairie Server source blob the Live TV subsystem imports or adapts.

### Clients, protocols and compatibility

- [How Bloem's native API is organized](../architecture/bloem-client-surface.md) — why Bloem-only features live under `/api/bloem/v2`, and the native document set.
- [How Bloem's SDKs and generated contracts fit together](../architecture/bloem-sdk-compatibility.md) — plugin SDK pins, generated client DTOs and consumer synchronization.
- [How the client contract generator works](../architecture/bloem-client-contract-generator.md) — the registry, the Kotlin and Swift files the apps copy, the contract digest, and the rules that keep older apps working.
- [How Bloem runs Silo's Jellyfin and Audiobookshelf servers](../architecture/bloem-protocol-compatibility.md) — Silo's Jellyfin and Audiobookshelf servers, their tenant wiring and organization rules.
- [How remote control works](../architecture/admin-remote-control.md) — invariants for server-issued commands to client apps.

### Embedded web

- [What the web app covers](../architecture/bloem-web-feature-coverage.md) — Bloem features in Silo's web app: implemented screens, authority boundaries, verification and limits.
- [How Bloem's web text is translated](web-messages.md) — Bloem's message catalog beside Silo's: where the text lives, keys and the lazy admin part, adding a message, translations, the generator, the coverage test and the pseudo-locales.

## API references and specs

- [Bloem native API reference](../bloem-api-reference.md) — the maintained wire reference for Bloem's native extensions.
- [Organization live updates](../design/bloem-org-live.md) — the ticket route and WebSocket (`bloem-live.v1`) that tell an organization administrator's app when streams, people or activity changed; auth by the context pass, scope, close codes, bursts.
- [Playback cards: who calls which route](../playback-overlays-api.md) — campaign CRUD and the save-to-inbox endpoint for playback overlays.
- [How Bloem announces itself on a home network (`_bloem._tcp`)](../specs/lan-service-advertisement.md) — the opt-in DNS-SD service (`internal/lanadvert`) Bloem's clients browse for.

## Other Bloem-owned files

- [How audiobooks and podcasts work in Bloem](../../internal/audiobooks/README.md) — package notes for Silo's audiobook code and its Audiobookshelf-compatible server in Bloem.
- [Bloem's brand assets](../../assets/brand/README.md) — vector sources, palette and where the rendered icons live.

## Upstream documents that still carry Bloem edits

The playback schema files below are seams (`contracts/seams.txt`) because tooling reads them.

- `docs/design/schemas/playback-v3/v3/**` — playback schema and fixtures validated by `make verify-playback-fixtures`.
