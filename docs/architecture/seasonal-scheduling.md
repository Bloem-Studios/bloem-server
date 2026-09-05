# Seasonal scheduling

Garden's Seasonal effects page manages the selected linked server's ambience registry. Schedules are stored on that media server, so Garden need not stay online for activation or expiry. This is independent of basic server announcement authoring.

The admin ambience list advertises `yearly_scheduling: true`. A schedule window accepts `starts_at`, `ends_at`, `repeat_yearly` and an IANA `timezone` (default UTC). Annual intervals must be shorter than one year and cannot activate before the first start. Each occurrence preserves the original local month, day and time in that timezone, including seasons crossing New Year. Missing calendar dates such as February 29 in a non-leap year, and wall times skipped by daylight saving, skip that occurrence. The end is exclusive.

The server resolves recurring windows before projecting them to clients. Client-facing ambience windows remain concrete start/end UTC instants; recurrence fields are omitted. Existing clients, including Apple clients, do not need to implement a recurrence interpreter. Jellyfin compatibility endpoints are unchanged; this is a Bloem presentation feature.

The web client renders snow on Home and sign-in and suppresses it while video playback is active. It polls active branding every 30 seconds on those surfaces, re-evaluates expiry locally, honors reduced motion, and offers a device-local off toggle. Android uses the same concrete windows, polls only while the surface is resumed, and offers its toggle under Profile. Existing artwork schedule fields remain available for clients with artwork renderers; this change adds the snow renderer only.

Publication or removal is visible on the next client refresh. Scheduled expiry does not require another network response. This distinction matters when Garden or the media server becomes unreachable.

The schema change is additive and uses Goose migration `20260905090908_ambience_annual_schedule.sql`. Apply it with the normal server migration procedure before deploying the server implementation. Runtime smoke testing and database-backed recurrence tests require a disposable PostgreSQL environment; focused calendar tests do not substitute for that gate.
