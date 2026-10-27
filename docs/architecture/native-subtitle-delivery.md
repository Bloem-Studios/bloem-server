# Native client subtitle delivery

> **Provenance and status** (checked 2026-10-27 against `main` at `e852f92bf`):
> historical integration record. The behavior is Silo's: it comes from Silo
> `658be10eb` (#938, 2026-09-04). The
> [September 8 reconciliation](2026-09-08-upstream-reconciliation.md) took that
> commit only partly, and Bloem's `549d88b6d` (2026-09-08) took the rest early and
> added this page. Later full Silo merges brought the same code in as Silo's own:
> `stream_subtitle_identity.go`, `stream_subtitle_offset.go` and
> `subtitle_identity_v3.go` are now Silo files. The remaining Bloem difference in
> `internal/playback/protocol_v3.go` is the `negotiated_client_features` field,
> which is recorded in the seam ledger. The current description of
> `embedded_subtitles_v1`, `timing_origin_seconds` and `subtitle_embedded_failed`
> is Silo's [playback protocol v3](playback-protocol-v3.md#8-track-identity-and-the-subtitle-ordinal-space).
> The Apple and Android client statements below come from those clients'
> repositories at the time and cannot be checked from this repository.

Bloem adopts the remaining subtitle reliability changes from Silo commit
`658be10eb03615f104790fba0431a4d19fd02d15`. This completes the server-side
subtitle work deferred by the September 8 upstream reconciliation.

## Artifact and timeline contract

`subtitle.artifact.format` and `mime_type` describe the served bytes, not the
catalog codec. SRT/SubRip and mov_text converted to WebVTT report `vtt` and
`text/vtt`. Full subtitle artifacts use absolute source timestamps and
`timing_origin_seconds: 0`, including resumed and reanchored video streams.
Subtitle URLs pin the selected file and embedded, external, or downloaded track
identity. Reordering inventory cannot redirect an issued URL to another track.

Apple v3 advertises its text sidecar renderer on original, progressive and HLS
routes. Its caption surface maps the player clock to source time through the
plan timeline before evaluating cues. It requests compatible subtitle fidelity;
ASS styling and bitmap rendering are not advertised by this renderer.

Android v3 uses the selected artifact URL and format when the plan renders or
converts a track, retaining the inventory representations for other tracks.
This prevents an ASS inventory entry from overriding a selected VTT conversion.
Sidecar cue time is source time minus the video's timeline offset plus the
viewer's subtitle delay. Embedded player cues receive only the viewer delay.

Both clients vendor generated types from the same server source revision. Their
regressions cover resumed source-time cues; Android also covers converted
artifact selection and preservation of unselected inventory URLs.

## Negotiated embedded selection

The server supports `embedded_subtitles_v1` and exact container/codec/track-ID
attestations on `original_http`. A native selection and an artifact are mutually
exclusive. A confirmed `subtitle_embedded_failed` recovery disables native
selection for the remainder of that playback attempt, including capability
refreshes. Adapted routes use a sidecar or burn-in fallback.

The current Apple v3 and Android v3 adapters do not advertise the new native
embedded attestation: they continue using their supported artifact renderers.
The legacy `embedded_text` hint is not an exact stream-identity guarantee.
Enabling native selection in an adapter requires proof that it selects the
specified identity and reports failures through the negotiated recovery path.

## Verification boundary

Regression tests cover artifact representation and origin, route/track identity,
native capability validation and recovery, subtitle extraction and offsetting,
web loading/retry behavior, and both v3 client subtitle consumers. These are
source and local automated checks, not a deployment or a physical-device
playback certification. The tests and fixtures are listed in commit `549d88b6d`.
