# LAN service advertisement (`_bloem._tcp`)

**Status: NOT BUILT.** Every client browses for this service. No server has ever published it.
LAN discovery therefore finds nothing on any platform today, and has never worked.

Discovered 2026-09-02 while aligning clients after the native API moved to `/api/bloem/v1`.

## What exists

`bloem-android`, `bloem-android-skyline` and `bloem-apple` all browse `_bloem._tcp` and parse a
TXT record. Nothing in `bloem-server`, upstream `silo-server`, `bloem-ops` or `bloem-edge`
publishes it — there is no mDNS, Bonjour, zeroconf or avahi registration anywhere in the estate.

Clients degrade to manual origin entry, which is why the gap went unnoticed.

## The TXT contract the clients already implement

Service type `_bloem._tcp`, on the port the API is served from.

| Key | Value | How clients read it |
|---|---|---|
| `txtvers` | `1` | Gates whether the rest is interpretable. A value a client does not recognise discards the whole record rather than trusting it partially. Absent is read as `1`. |
| `id` | advertised server identifier | Feeds candidate construction; must match `server_id` from the identity probe, which is authoritative. |
| `name` | display name | Cosmetic; clamped and sanitised client-side. |
| `scheme` | `http` or `https` | Validated through the client's origin rules. Cleartext to a non-loopback host is refused. |
| `api` | comma-separated Silo-compatible majors, i.e. `1` | Silo-compatible majors ONLY. It cannot answer "does this speak the native API" — upstream Silo serves its own major 2 — so a record carrying only `api` never causes a client to withhold a candidate. |
| `bloem` | comma-separated native surface versions, i.e. `v1` | The record-level counterpart of the identity document's `bloem_api`. A parseable list naming no surface the client speaks is the one reason a client withholds an attempt early. |

The advertisement is an unauthenticated hint. It can only ever push a candidate toward
"not worth attempting"; it never grants trust. `GET /api/bloem/v1/server/identity` remains the
authority on whether a connection is made.

`bloem` is a new key: no client shipped before 2026-09-02 reads it, and every client treats an
absent key as "cannot determine", so adding it is backward compatible.

## What building it needs

1. **A zeroconf dependency.** `go.mod` has none today. Whatever is chosen has to answer
   conflicting-name and interface-change cases, not just announce once at boot.
2. **A deployment story.** mDNS needs the host's L2 broadcast domain. `docker-compose.livetv.yml`
   already uses `network_mode: host`, so at least one supported deployment can do it; the LXC
   deployments and any bridge-networked compose cannot without configuration. The advertiser
   must be opt-in and must degrade silently where multicast is unavailable rather than logging
   on a timer.
3. **A settings surface**, consistent with how Live TV tuner discovery is exposed
   (`docs/livetv-tuner-discovery.md`), including an off switch — operators on shared or hostile
   networks will not want the server announcing itself.
4. **The identity values wired from one source.** `api` must come from
   `serverAPIMajorVersions` and `bloem` from `nativeAPISurfaces`
   (`internal/api/handlers/server_identity.go`), never be typed as literals, or the record and
   the identity document will drift.

## Prior art in this repository

Live TV tuner discovery already broadcasts and receives on the LAN over UDP 65001
(`docs/livetv-tuner-discovery.md`), so LAN behaviour is not new ground here — but it is
outbound probing, not service registration, and shares no machinery with this.
