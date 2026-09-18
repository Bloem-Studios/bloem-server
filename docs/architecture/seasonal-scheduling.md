# Seasonal scheduling

Platform administrators can manage the ambience registry in the embedded web's
**Campaigns & seasonal packs** page. Garden's Seasonal effects page can manage the
selected linked server's same registry. Schedules live on the media server, so neither
authoring UI must stay online for activation or expiry. This is independent of basic
announcement authoring. Native writes require platform context; organization admins
cannot publish packs. Editors review publication, confirm deletion and read back
attempted writes without replay. The registry is last-write-wins, without revision locks.

The admin ambience list advertises `yearly_scheduling: true`. A schedule window accepts `starts_at`, `ends_at`, `repeat_yearly` and an IANA `timezone` (default UTC). Annual intervals must be shorter than one year and cannot activate before the first start. Each occurrence preserves the original local month, day and time in that timezone, including seasons crossing New Year. Missing calendar dates such as February 29 in a non-leap year, and wall times skipped by daylight saving, skip that occurrence. The end is exclusive.

The server resolves recurring windows before projecting them to clients. Client-facing ambience windows remain concrete start/end UTC instants; recurrence fields are omitted. Existing clients, including Apple clients, do not need to implement a recurrence interpreter. Jellyfin compatibility endpoints are unchanged; this is a Bloem presentation feature.

The embedded web client renders snow or validated banner/sprite artwork on Home and sign-in, and suppresses presentation while video playback is active. It polls every 30 seconds, re-evaluates expiry locally, honors reduced motion, and offers a device-local off toggle. Authenticated Home uses `GET /api/bloem/v1/ambience` when `seasonal_viewer_v1` is advertised. That route requires a verified profile in the active current organization and returns public packs plus that organization's packs. Pack content and target authorization share one database snapshot, including during retargeting. Sign-in and capability fallback use public-only branding. Requests and cached results are bound to session, profile and PIN authority. Android uses the same concrete windows, polls only while the surface is resumed, and offers its toggle under Profile; this web change does not implement new native-client delivery.

Publication or removal is visible on the next client refresh. Scheduled expiry does not require another network response. This distinction matters when Garden or the media server becomes unreachable.

Seasonal asset upload currently requires public S3 storage. Local catalog/branding
artwork storage does not supply the ambience service. Without public S3, authoring
still supports HTTPS artwork references, but the registry reports uploads unavailable
and the upload endpoint returns `503`.

Configured-S3 acceptance passed for banner/sprite uploads and decoded Home images,
exact image bytes/MIME/dimensions, ETag revalidation, input rejection and current-organization
versus public filtering. Temporary assets and storage configuration were removed and
the original settings restored. This does not imply that every deployment has S3
configured; see [storage setup](../s3-storage-setup.md) and the
[coverage evidence](bloem-web-feature-coverage.md#acceptance-evidence-and-limits).

The schema change is additive and uses Goose migration `20260905090908_ambience_annual_schedule.sql`. Apply it with the normal server migration procedure before deploying the server implementation. Runtime smoke testing and database-backed recurrence tests require a disposable PostgreSQL environment; focused calendar tests do not substitute for that gate.
