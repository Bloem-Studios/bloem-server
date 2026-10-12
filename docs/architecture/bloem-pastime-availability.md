# Managed profile availability lookup

The optional `bloem.availability.v1` extension adds a bounded movie/series provider-ID lookup to the existing installation-bound managed-tracking broker. It exposes no filesystem paths, source URLs or playback credentials. Pastime supplies request identifiers and matching provider identifiers through the backend plugin. An availability result is informational, never a playback grant.

The host uses the ordinary viewer resolver and catalog access filter, checks playback permission, excludes missing files and metadata-only entries, and requires active native-source configuration and selected-root membership. Duplicate provider identities return unavailable. Current grants, profile revisions and visibility are checked before lookup and again before publication. Calls do not retain grant locks during lookup, so revocation can proceed. Results expire within two minutes.

The plugin polls Pastime's bounded queue of titles requested by connected profiles. Pastime binds responses to their original request identifiers and checks current profile, instance and service authority. Reads remain account/profile private. Playback links use the destination's existing administrator-controlled handoff settings and let Bloem perform normal authentication and playback authorization.

This extension is implemented in Bloem-owned broker and API adapter files. The ordinary Silo watch-sync provider, native public contract and scheduler remain unchanged.

## Personal recommendation feedback

The existing installation-bound recommendation service additionally accepts
monotonic per-profile/content Dismiss and Undo choices. Pastime owns the durable
local choice and retries the latest revision until the broker confirms it. A
late receipt cannot acknowledge a newer Undo. Bounded pending batches rotate
past failed choices so one profile cannot starve other profiles.

The host rechecks installation generation, enrolled profile and policy before
committing. Initial feedback must name a currently authorized personal model
result; retries and Undo can name that profile's existing feedback record.
Changed payload at the same revision is a conflict; an older revision cannot
rewind the choice. No watched, dropped, rating or collection state is changed.

An owned reader adapter applies the returned exclusions to Bloem's personal
model rows after ordinary catalog policy. Popular and collaborative rows retain
upstream behavior. The recommendation engine and model weights are unchanged.
Existing Apple/Android card responses retain their shape and require no client
contract change; Jellyfin compatibility has no equivalent personal AI surface.
