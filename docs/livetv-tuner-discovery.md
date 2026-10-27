# Live TV tuner discovery (HDHomeRun + Dispatcharr)

> **Provenance.** Live TV is not a Silo feature; Silo lists it as a non-goal. Bloem's
> tuner, guide, streaming and DVR subsystem, and the first version of this page, were
> adapted from [Prairie Server](https://github.com/Prairie-Server/prairie-server) at
> commit `095ecd22fbea3384a905eb9049386015db3ff4d8` (AGPL-3.0) in `ef2d38171`
> (2026-08-13). [`docs/livetv/prairie-source-manifest.tsv`](livetv/prairie-source-manifest.tsv)
> lists every imported file with its source blob; it classifies this page and
> `docker-compose.livetv.yml` as `adapted` (product copy changed from Prairie to
> Bloem). See [FORK.md](../FORK.md#prairie-server-live-tv). Later Bloem changes:
> the operational routes moved to the native `/api/bloem/v1` prefix in `9cc9d9326`
> (2026-09-14), and Xtream providers were added in `23ee4c1ac` (2026-09-19), which is
> Bloem's own addition, not Prairie's.
>
> **Status.** Checked on 2026-10-27 against Bloem `main` at `e852f92bf` (Silo through
> `3e2d306b2`): `internal/livetv/hdhomerun/`, `internal/livetv/fetch_url.go`,
> `internal/api/router_livetv.go`, `docker-compose.livetv.yml` and
> `web/src/pages/AdminLiveTV.tsx`. Source review only; this refresh did not run a
> tuner.

Bloem can discover Live TV tuners from **Admin → Live TV → Tuners**. Live TV
administration is a platform page: choose the **Platform** administrative context
first (organization contexts do not list it).

| Mode | What it finds | Requirements |
| --- | --- | --- |
| **Discover on LAN** | SiliconDust HDHomeRun (UDP broadcast to port `65001`) and any Dispatcharr instance that answers HDHR UDP discovery | Host must be able to broadcast/receive on the LAN |
| **Probe URL only** | Dispatcharr HDHR emulation (`/hdhr/discover.json`) or a direct HDHR `discover.json` | Reachable HTTP(S) URL |

Candidates are verified over HTTP before you click **Add**. Existing tuners are marked **Added**.

Manual add takes a single **Tuner URL** (base URL or host). Bloem probes the usual `discover.json` locations, including Dispatcharr’s `/hdhr/` path, then stores the device identity and base URL from the response. You do not need a separate discover URL or device ID.

## Xtream is a separate setup flow

Authenticated Xtream live providers are added with the **Xtream live TV provider**
form in the same Tuners tab, not through LAN discovery, URL probe or generic tuner
creation. Supply the provider base URL and separate credentials, then attach its
XMLTV guide by selecting the saved provider. Xtream needs outbound HTTP(S), not
HDHomeRun UDP discovery or the host-network override below. Prefer HTTPS.

See [operator setup](wiki/admin-guide.md#210-live-tv) and the
[Xtream contract and limits](architecture/xtream-live-tv.md). Live TV administration
requires an authenticated administrator with the selected primary profile and any
required PIN verification; a delivery ticket cannot authorize it.

## Docker: enable LAN UDP discovery

Default `docker-compose.yml` runs Bloem in **bridge** networking. Bridge mode typically **cannot** send SiliconDust discovery broadcasts to your LAN, so **Discover on LAN** returns no devices (URL probe still works).

### Linux: use the Live TV compose override

```sh
docker compose -f docker-compose.yml -f docker-compose.livetv.yml up -d
```

Or set in `.env`:

```dotenv
COMPOSE_FILE=docker-compose.yml:docker-compose.livetv.yml
```

What the override does:

- Sets `network_mode: host` on the `silo` application service and clears its
  published ports
- Points `DATABASE_URL` / `REDIS_URL` at `127.0.0.1` (postgres/redis still publish
  their ports on the host's loopback address)
- Keeps the application's base listener on host port `8080`; the base `PORT`
  variable (default `8090`) applies only to bridge-mode host publishing, so clients
  and any reverse proxy must point at `8080` while the override is active

You can combine with NVIDIA:

```sh
docker compose -f docker-compose.yml -f docker-compose.nvidia.yml -f docker-compose.livetv.yml up -d
```

### Docker Desktop (macOS / Windows)

`network_mode: host` does **not** attach the container to your real LAN. Prefer:

1. **Probe URL** with your Dispatcharr base (`http://dispatcharr.local:9191`) or HDHR IP (`http://192.168.1.50`)
2. Or run Bloem **from source on the host** (not in Docker) if you need UDP discovery

### Bare metal / from source

No special config — UDP discovery uses the host network stack directly.

## Dispatcharr

Dispatcharr exposes HDHomeRun-compatible endpoints (commonly under `/hdhr/`). For a
bare host or base URL, Bloem probes, in order
(`internal/livetv/hdhomerun/probe.go`):

- `{base}/hdhr/discover.json`
- `{base}/discover.json`
- `http://{host}:9191/hdhr/discover.json` and `https://{host}:9191/hdhr/discover.json`,
  unless the given port was already `9191`

A URL that already ends in `/discover.json` is used as given, and one ending in
`/hdhr` gets `/discover.json` appended.

No Dispatcharr API key is required for HDHR emulation; protect it with network ACLs as you would any tuner.

## API

The tuner routes are mounted by `mountLiveTVRoutes` (`internal/api/router_livetv.go`)
behind the acting-administrator gate; viewer routes such as channels, guide and
recordings are separate and described in
[Live TV client access](architecture/live-tv-client-access.md).

`POST /api/bloem/v1/livetv/tuners` accepts:

```json
{ "url": "http://192.168.1.50" }
```

Legacy `discover_url` and `device_id` fields are still accepted as aliases for the
same address. Provider credentials (`username`, `password`, `max_connections`) are
refused on this route. The SiliconDust hardware id is read from
`discover.json` and stored as `device_id` on the tuner; clients do not supply it. A
body with `"type": "xtream"` is refused with `400 invalid_argument`; Xtream providers
use `POST /api/bloem/v1/livetv/tuners/xtream`.

`POST /api/bloem/v1/livetv/tuners/discover` takes an optional body
`{ "timeout_ms": 2000, "include_udp": true, "probe_urls": ["http://dispatcharr.local:9191"] }`.
`timeout_ms` bounds the UDP listen window (default 2000, maximum 10000).

## Security notes

- Discovery and probe only accept `http://` / `https://` URLs (same Live TV fetch allowlist as manual add).
- URL validation and dial-time checks reject credentialed URLs, loopback and
  cloud-metadata destinations, including names that resolve to them
  (`internal/livetv/fetch_url.go`). Use a reachable permitted LAN address; a
  loopback fixture is not a supported tuner deployment. Do not disable these guards
  for acceptance testing.
- Do not expose HDHR/Dispatcharr discover or stream URLs to the public internet.
- Tuner listing, discovery, add, scan and removal are administrator-only.

Operational Live TV routes use the native Bloem prefix. See
[client access](architecture/live-tv-client-access.md) for viewer grants, signed HLS,
recording ownership and DVR limits. Actual tuner/Safari and multi-replica owner-loss
acceptance remain separate from offline decoding and synthetic HTTP fixtures.
