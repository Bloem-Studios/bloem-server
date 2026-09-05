# SQLite bridge preflight

The bridge release requires a one-way SQLite-to-PostgreSQL import before SQLite
retirement at 1.0. `cmd/sqlite-bridge-preflight` implements only the source inventory
milestone. It does not migrate schemas, import rows, connect to PostgreSQL, select a
backend, or certify that switching backends is safe.

Commands assume the repository root is the working directory:

```sh
go run ./cmd/sqlite-bridge-preflight --manifest
go run ./cmd/sqlite-bridge-preflight --source "$OFFLINE_BACKUP/7.db" --account-id 7
```

The source must be an existing regular file named after the positive central
account ID. The filename check is necessary but does not prove that a file belongs
to this installation or account. That provenance must be verified against the
central account database and backup record before import.

Use a standalone offline SQLite backup captured after stopping all writers, or
through a verified SQLite backup mechanism. The command refuses nonempty WAL and
rollback journals instead of silently inspecting only the main file. It opens the
source with `mode=ro`, `immutable=1`, and query-only mode, so SQLite cannot create
shared-memory files or upgrade the database. Do not pass a live database: immutable
mode assumes the input is stable. Before/after metadata and sidecar checks detect
some changes but cannot prove snapshot consistency. A main-file hash would not
prove that committed WAL state was included, so this command issues no backup
fingerprint or consistency certificate.

JSON output contains the schema version, known table names, row and column counts,
and blockers. It excludes paths, profile names, IDs, values, PIN hashes, SQL error
messages, and unknown table names. Unknown tables are represented with a redacted
label and block import even when empty. Integrity errors stop inspection. Missing
tables, schema versions other than the current version 22, and nonempty legacy
session/download tables are blockers. Old sources are never upgraded in place.

The manifest covers all 29 current persistent application tables. Schema 21 remains
an old source: its missing revision tables and version block import, and inspection
does not create the tables or upgrade its version. Manifest rules describe
required mappings, not implemented transformations. In particular:

- Preserve account-scoped profile, collection and history IDs, visibility,
  restrictions, timestamps, ordering, history identities and hidden cutoffs.
- Preserve all six canonical settings scopes, null/false/empty distinctions,
  revisions, mutation replay records and migration rejects. Allocate destination
  integer surrogate IDs where required, without changing semantic identity.
- Group section overrides across profiles into PostgreSQL's legacy settings JSON
  representation without losing IDs, flags or timestamps.
- Explicitly classify nonempty legacy SQLite session/download rows. They cannot be
  copied into unrelated modern central tables.
- Map `personal_collection_revisions` to PostgreSQL `user_collection_revisions`
  with account identity. Retain revision tombstones for deleted collections;
  absence of a live collection is not permission to discard its witness. Map
  `personal_collection_order_revision` singleton 1 to `user_collection_order_revisions`
  by account ID, not profile or group. The later writer must reconcile source
  witnesses with destination trigger increments and existing revisions before
  enabling validators, so old ETags cannot accidentally become valid again.
  Inventory does not implement that reconciliation.
- Define progress sync sequence handling before clients reconnect; SQLite's
  per-file sequence cannot be copied blindly into PostgreSQL's global sequence.

The inventory always reports `ready: false`. The command exits 2 after producing an
inventory, 1 on inspection/output failure, and 0 only for `--manifest`. `go run`
wraps the executable's exit status; scripts that need these exact codes should
build and invoke the binary. Table presence and counts are not completeness proof.
Column mappings, local and central reference validity, target collisions, semantic
read equality, and all-account coverage are still unverified.

The next milestone must settle conflict and legacy-disposition rules, implement
an atomic account transaction with a durable import receipt, and verify replay,
lost acknowledgements, interruption and whole-backup recovery. Only after every
account is verified, progress cursors are safe, and all nodes use the same selected
backend can an explicit global switch proceed. Existing source files and SQLite
support remain through the bridge. This command implements none of those writes
or switching steps.
