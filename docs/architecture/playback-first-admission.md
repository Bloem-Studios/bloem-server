# PostgreSQL first playback admission

`playback-source-admission` can admit one existing account's current PostgreSQL
personal-data source. It does not start playback, enable the initial runtime,
create accounts, migrate a database, or change the configured provider. Plan mode
is the default. Apply is an explicit operational action.

All API processes that can launch legacy playback or write its personal state
must run the admission barrier before applying an intent. A mixed deployment with
older writers is unsupported: those writers do not participate in the gate.
This command does not establish fleet version agreement or restored-source
freshness. SQLite, source switching, restore/copy cutover, retirement, takeover
and executor replacement remain outside this protocol.

## Retained intent and decision

Prepare and retain a private JSON file before apply. Values must identify the
existing installation and account; generate the source and intent UUIDs once.
Use canonical lowercase nonzero UUIDs. An example with reserved identities:

```json
{
  "installation_id": "11111111-1111-4111-8111-111111111111",
  "account_id": 123,
  "expected_username": "test-account",
  "backend": "postgres",
  "source_id": "22222222-2222-4222-8222-222222222222",
  "intent_id": "33333333-3333-4333-8333-333333333333"
}
```

Commands assume the repository root is the cwd. Supply database credentials
through `SILO_ADMISSION_DATABASE_URL`; do not commit the intent or connection
information.

```sh
go run ./cmd/playback-source-admission --intent admission-intent.json
go run ./cmd/playback-source-admission --intent admission-intent.json --apply
```

The first command returns `eligible` without creating database rows. The second
returns `admitted` after committing the marker, registration and retained receipt
in one transaction. Repeating the same intent returns `already_admitted` and the
original admission timestamp. A changed intent, account name, installation,
provider, marker or registration refuses; the command never repairs a mismatch.

If a reply is lost, inspect using the unchanged intent file. An `eligible` result
means no matching admission was committed; it does not automatically repeat the
apply. `already_admitted` resolves the original decision without renewing rights
or allocating work. Never generate replacement UUIDs or undo markers to resolve
an uncertain result.

The operation requires an existing persisted installation identity and verifies
the configured PostgreSQL provider while holding the settings mutation lock. It
refuses existing source markers, registrations, playback attempt rows and sink
receipts. It does not delete or adopt retained authority, including expired rows.
Allow normal lifecycle retention cleanup to complete; this tool provides no
forced cleanup. Source selection generation is exactly one; the intent UUID is
the registration's admission UUID.

## Transition ordering

The account's PostgreSQL source advisory gate orders first admission against
legacy launch and playback-origin persistence. First admission holds the gate
exclusively; legacy operations hold it shared. The marker, admitting registration
and decision become visible together at commit. Existing progress/history rows
are preserved.

Native legacy start holds its gate through transport publication or rollback.
Jellyfin compatibility holds it through upstream creation, cold reconstruction,
route setup and response publication. It releases the connection before streaming
media bytes. Nested persistence joins the exact pool/account launch lease so a
queued exclusive transition cannot deadlock it. The lease waits for active nested
transactions when released. A delayed callback that retains the context after
release must obtain a new gate; the old context grants no lasting authority.

Native progress and watch-state playback stop/history explicitly mark their write
origin. The stop path includes expiry and crash finalization; Jellyfin progress
marks the same origin and shared native teardown reaches the stop service.
Notification wrappers preserve the context and admission capability. PostgreSQL
checks the source marker in the same transaction as each playback-origin write.
Once a marker exists, delayed unbound progress, hints and history refuse. Manual
watch-state changes and imports do not carry the playback marker and retain their
existing semantics. A multipart legacy stop may have committed an earlier step
before transition; later steps refuse. This does not claim an atomic legacy stop
receipt.

New unbound attempt reservations and executable legacy attempt saves refuse after
registration. Initial reservations carry the originally captured admission UUID;
the reservation persists it and exact retries cannot refresh it. Non-executable
terminal refusals remain persistable without creating a session or granting
execution. The initial protocol still installs the exact source fence before
execution and publishes durable authority before exposing the session.

Admission does not terminate already-running legacy media transfers or convert
them into bound attempts. Their later personal-state writes are fenced; subsequent
legacy launch/reconstruction requests refuse. Existing legacy streams should be
stopped before testing the admitted account. The initial runtime and supported
execution topology must be configured separately. A successful admission alone
is not an end-to-end playback result.
