# Bloem deployment — November 1, 2026: provider sign-out returns to its domain

Production has run `main` at `59616c4af19f1650c7c48358f27479aacde01845` since 11:29:01 UTC.
It replaces `8a721cff2` and adds `59616c4af`: a web sign-out through the OAuth provider
returns the browser to `/login` on the organization domain it started on, not the
server's public URL. On the public URL, and on an unregistered host, nothing changes.
Before a second organization's members use provider sign-out on their own domain,
`https://<domain>/login` must be registered with the provider as a post-logout redirect
URI. The change is one more line in the declared `internal/auth/oauth_handler.go` seam;
the seam count stays at 304.

The owner asked to push and deploy. Silo `main` was still at `63ef34b23`, already merged.
The image was built locally without waiting for CI.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, as for `8a721cff2`.
  Tagged `bloem-server:release-59616c4af` and loaded on CT135 and CT172. The binary
  `/usr/local/bin/silo` has SHA-256
  `aac8baeceee4bd20dd73eab22776447f03e6a35370789e00d369405a27fe0df2` on both hosts.
- **Before the restart:** container snapshot `pre-main-59616c4af`. Database restore
  point of the same name at LSN `426/437FAE88`. Segment `000000030000042600000043`,
  which holds it, was archived after a WAL switch, with no archive failures.
- **Restart.** The kit's restart script required the live image to be `8a721cff2`. One
  transcode was running, and the stop took 7 seconds (exit 0). No migrations were
  pending; 750 are applied. The server answered ready at 11:29:07, 18 seconds after the
  stop began, and was healthy with no restarts.
- The runtime fingerprint is unchanged apart from the image. `/api/v2/system/info`
  reports `59616c4a`, `/api/bloem/v1/capabilities` lists `organization_domains_v1`, and
  `GET /api/v2/auth/provider-logout` answers `401` without a session. No error lines were
  logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) were idle and
  now run `release-59616c4af`, healthy. The server marked the CT172 node unhealthy at the
  one health check during its restart and healthy from the next. Their previous compose
  files are kept with suffix `.bak-pre-59616c4af`.

## Verification before deploy

With `umask 022` and Go 1.26.10 from `go.mod`: a new test for the sign-out address on an
organization domain, off a registered domain and without a public URL; the
`internal/auth`, `internal/apiv2`, `internal/orgdomains`, `internal/api/...`, `cmd/...` and
`contracts/...` packages on a migrated database; the seam ledger (304), the changed-line
lint (no issues) and the local-path check. The whole Go suite last ran for `8a721cff2`;
see its [receipt](2026-11-01-organization-domains-deployment.md).

## Rollback

Start `bloem-server:release-8a721cff2` again and set the transcode nodes back from their
`.bak-pre-59616c4af` compose files. No schema changed.
