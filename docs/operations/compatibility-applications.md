# Jellyfin/Emby and Audiobookshelf Compatibility

Vondel's Jellyfin/Emby and Audiobookshelf compatibility surfaces are built
into this server. They are not separate applications, containers, or images
— there is nothing to install, enroll, or pull from a registry.

- **Jellyfin/Emby** — the Jellyfin/Emby protocol, served at the server's own
  canonical address (`https://vondel.example/`, same as the native web app
  and API). Real third-party apps confirmed working against it include
  VidHub, Findroid, and Infuse.
- **Audiobookshelf** — the Audiobookshelf protocol, served at
  `https://vondel.example/abs`.

Both are **enabled by default** and reachable **on the server's own address —
no extra ports, no reverse-proxy rules, no separate host to run.** Either can
be turned off from **Admin → Settings → Compatibility** if you don't need it.

## How it works

Requests to the Jellyfin/Emby and Audiobookshelf protocol path families are
matched by a fixed, reviewed routing table and dispatched directly, in the
same process, to Vondel's own built-in Jellyfin-compatible and
Audiobookshelf-compatible handlers — the same request-hardening (path
traversal checks, size limits) applies as everywhere else on the server, but
there is no separate service, no network hop, and no enrollment lifecycle
involved. Turning a compatibility surface off in settings makes its paths
answer `compatibility_unavailable`; it does not stop or remove anything,
since there is nothing separate running to stop.

## Advanced: a dedicated port

Some operators run a reverse-proxy setup built around a specific port rather
than the server's main address. Both surfaces can still be given their own
dedicated listener as an opt-in, alongside the default same-origin path —
set `JF_PORT`/`ABS_PORT` in `.env` (defaults `8096`/`13378` if you set the
variable without a value) and uncomment the matching port mapping in
`docker-compose.yml`. This is an addition, not a replacement: the same-origin
paths keep working either way.

## About the compatibility gateway

This server also ships a general-purpose gateway (`internal/compatgateway`)
capable of proxying reviewed, fixed path families to a separately enrolled,
trust-negotiated companion application over the network, with a hardened
default-deny posture (structural Compose scanning, mutual TLS for remote
placement, scoped service credentials, no shared data planes). It exists as
real, tested infrastructure for that kind of extension in the future.
**No companion applications currently exist or ship** — Jellyfin/Emby and
Audiobookshelf compatibility do not use it; they are served in-process, as
described above. There is no enrollment flow to follow for either.

## Troubleshooting

| Symptom | Meaning | Operator action |
| --- | --- | --- |
| A Jellyfin/Emby or Audiobookshelf client can't connect | The corresponding compatibility surface is turned off | Admin → Settings → Compatibility, confirm it's enabled |
| A client that previously worked via a dedicated port stops working | `JF_PORT`/`ABS_PORT` was unset, or the compose port mapping was removed | Same-origin access (the default) is unaffected either way; re-add the port mapping and env var only if you specifically need the dedicated listener |
