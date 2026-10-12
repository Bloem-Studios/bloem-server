# Managed profile availability lookup

The optional `bloem.availability.v1` extension adds a bounded movie/series provider-ID lookup to the existing installation-bound managed-tracking broker. It exposes no filesystem paths, source URLs or playback credentials. Pastime supplies request identifiers and matching provider identifiers through the backend plugin. An availability result is informational, never a playback grant.

The host uses the ordinary viewer resolver and catalog access filter, checks playback permission, excludes missing files and metadata-only entries, and requires active native-source configuration and selected-root membership. Duplicate provider identities return unavailable. Current grants, profile revisions and visibility are checked before lookup and again before publication. Calls do not retain grant locks during lookup, so revocation can proceed. Results expire within two minutes.

The plugin polls Pastime's bounded queue of titles requested by connected profiles. Pastime binds responses to their original request identifiers and checks current profile, instance and service authority. Reads remain account/profile private. Playback links use the destination's existing administrator-controlled handoff settings and let Bloem perform normal authentication and playback authorization.

This extension is implemented in Bloem-owned broker and API adapter files. The ordinary Silo watch-sync provider, native public contract and scheduler remain unchanged.
