# Bloem deployment — November 1, 2026: organization domains and branding

Production has run `main` at `8a721cff278d447b5ae328a8478a7b782f4a8010` since 10:56:00 UTC.
It replaces `f14332386` and adds:

- [organization domains and branding](../architecture/bloem-organization-domains.md)
  (`b95a3dda9` to `e78436547`, `4d9258304`). Each organization can be served on its own
  domains, with its own name, logo and colours. Its emails link to its primary domain.
  An account signing in on another organization's domain is refused with
  `409 wrong_organization_domain`, which names where it signs in. An organization
  without a domain still signs in at the server's public address;
- `9181ef630`: a web sign-in through the OAuth provider stays on the organization domain
  it started on. This adds the 304th seam, `internal/auth/oauth_handler.go`. Each
  domain's callback addresses must be registered with the provider before its members
  use provider sign-in there;
- `45abcda81`: the web app's signed-out seasonal layer reads
  `GET /api/bloem/v1/ambience/public`, so the web app calls none of Silo's `/api/v1`;
- `8a721cff2`: the route goldens record that public route.

The owner asked to push and deploy. The image was built locally without waiting for CI.
Silo `main` was still at `63ef34b23`, so there was nothing new to merge.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, with the Bloem Image
  workflow's build arguments and OCI labels and the web bundle from the same archive.
  Tagged `bloem-server:release-8a721cff2` and loaded on CT135 and CT172. The binary
  `/usr/local/bin/silo` has SHA-256
  `a6a0e1359b9948144dca84f1a82a3c432f5776ff6fe892d22ec5d9f0985697bd` on both hosts.
- **Before the restart:** container snapshot `pre-main-8a721cff2`. Database restore
  point of the same name at LSN `425/4F777578`. Segment `00000003000004250000004F`,
  which holds it, was archived after a WAL switch, with no archive failures.
- **Restart.** The kit's restart script required the live image to be `f14332386`. One
  transcode was running, and the stop took 9 seconds (exit 0). Migration
  `20261101055208_bloem_organization_domains` added two empty tables in about one
  second; 750 migrations are applied and none pending. The server answered ready at
  10:56:07, 18 seconds after the stop began, and was healthy with no restarts.
- The runtime fingerprint is unchanged apart from the image. `/api/v2/system/info`
  reports `8a721cff`, and `/api/bloem/v1/capabilities` lists `organization_domains_v1`.
  `GET /api/bloem/v1/ambience/public` answers `200`, and the platform domain routes
  answer `401` without a session. No error lines were logged after the start; the one
  warning was the S3 provider refusing CORS on the private bucket.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) were idle and
  now run `release-8a721cff2`, healthy, and the server lists both as healthy. Their
  previous compose files are kept with suffix `.bak-pre-8a721cff2`.
- **The default organization's domain.** Production has one organization, the default.
  The server's public address was registered to it as its primary domain at 10:57:38
  (source `platform`), with the same insert the store makes. After the directory's
  30-second refresh, that address serves the same branding as before, byte for byte, the
  same page title, and an ordinary `401` for a wrong password.

## Garden

Garden `main` at `d9f77cc2` was deployed at 11:04:33 UTC with `scripts/deploy-lxc.sh`,
after its deploy gate passed for that commit and a `pg_dump` of its database
(`release-d9f77cc2`, 306 tables with data, migration 385, no new migrations). It gives a
Bloem tenant's members the tenant's primary domain as their sign-in address, and adds
`bloem-garden admin tenant-domain list|add|primary|remove`. It answered ready, with no
warnings in its log.

## Verification before deploy

Done locally, with `umask 022` and Go 1.26.10 from `go.mod`:

- the whole Go suite on PostgreSQL started with `max_connections=300` and
  `max_locks_per_transaction=512` and Redis for the storage tests. 232 packages passed.
  `internal/api` failed only on the two route goldens, regenerated in `8a721cff2`.
  `internal/historyimport` and `internal/markers` failed once under load. Neither is
  touched by this change, and both passed three runs alone and a full package run;
- all 437 database pins, on their own prepared database;
- the API, domain, `cmd` and contract packages again after the last two commits, the
  changed-line lint, and the local-path check.

## Rollback

Start `bloem-server:release-f14332386` again and set the transcode nodes back from their
`.bak-pre-8a721cff2` compose files. The two new tables can stay: the previous image does
not read them. Restoring the database to the restore point together with the container
snapshot also rolls back, but discards everything written since 10:55 UTC.
