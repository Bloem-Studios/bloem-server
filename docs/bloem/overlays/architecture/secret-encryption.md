# Bloem overlay: At-rest credential encryption

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which includes Silo through `63ef34b23`. It lists what Bloem
> adds to or renames in Silo's
> [`docs/architecture/secret-encryption.md`](../../../architecture/secret-encryption.md),
> which stays exactly as Silo wrote it. Silo's cipher, `SECRET_KEY` handling,
> backfill, key-loss and API-key-verifier text applies as written. The
> section-by-section changes are in the [For developers](#for-developers) section.

Silo encrypts the passwords and keys it stores for other services, such as
download managers and S3 storage, with the server's secret key. Bloem uses the same
encryption for one more kind of secret: the username and password of an **Xtream
provider**, a paid internet TV service that Bloem's Live TV can take live channels
from. Bloem holds those to a stricter rule than Silo's older secrets.

## What Bloem changes

- **No unencrypted fallback.** Silo still reads some older secrets that were stored
  before encryption existed. An Xtream provider's username and password are only
  ever stored encrypted, and anything else is refused.
- **Tied to their provider.** The encrypted value is bound to its tuner and the
  provider's address, so it cannot be moved to another source.
- **Never shown.** The values are decrypted only to talk to the provider. They
  never appear in what the server sends to apps, in the browser or in FFmpeg's
  arguments.
- **Keep the secret key.** There is no way to edit or re-encrypt these values;
  losing `SECRET_KEY` means adding the provider again.

## Words used in the code

| On this page | In the code and database |
| --- | --- |
| The server's secret key | `SECRET_KEY` |
| An Xtream provider's credentials | `bloem_livetv_xtream_credentials.credentials` (must start `enc:v1:`) |
| Xtream sources | migration `20260919133349_bloem_xtream_sources` |

## For developers

### Where this overlay came from

Bloem's Live TV can consume Xtream live providers. Their usernames and passwords
are encrypted with Silo's cipher but, unlike Silo's older integration
credentials, never accept a plaintext fallback (migration
`20260919133349_bloem_xtream_sources`, 2026-09-19). Bloem first documented this in
the Silo file in September 2026 and moved it here in `47ea7c889` (2026-09-23).

It was reviewed on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf`, when a replacement of Silo's first “Rollback / downgrade hazard”
paragraph that only reworded “predates this change” was dropped; Silo's text
applies. From `3e2d306b2` through `63ef34b23` Silo changed the document once, in
`07c05cad6` (#3309, 2026-10-31): administrators can no longer set the JWT signing
keys, and a downgrade clears both the `auth.jwt_secret` and
`audiobooks.abs.jwt_secret` rows. That applies to Bloem unchanged, and every
passage this overlay points at is unchanged.

### Section by section

---

**Location:** section “How it works”, the bullet “**Read path:**”. **Bloem
replaces** its label with “**Legacy integration read path:**”. The behavior
it describes (empty stays empty, non-`enc:v1:` values pass through as legacy
plaintext, any `enc:v1:` failure is an error) applies to Silo's integration
credentials only. Bloem's Xtream credentials below have no plaintext path.

---

**Location:** section “How it works”, after its bullet list. **Bloem adds:**

#### Bloem Xtream credentials

Xtream uses the existing cipher but **never accepts legacy plaintext**. Username
and password are encrypted together in
`bloem_livetv_xtream_credentials.credentials`, bound to the tuner row and canonical
provider base URL. Missing cipher wiring, invalid envelopes and mismatched bindings
fail closed; there is no plaintext backfill or fallback for this new table.

Credentials are decrypted only for fixed reviewed provider requests. They do not
appear in public DTOs, persisted channel URLs, React state, query/mutation caches,
browser storage or FFmpeg arguments. Encoder input is an owned MPEG-TS pipe.
Prefer HTTPS: at-rest encryption does not protect an HTTP request to the provider
from network observers.
See [Xtream live providers](../../../architecture/xtream-live-tv.md) for the complete boundary.

The September 19 migration adds credential storage, not key rotation. Atomic cipher
publication during service wiring is not a runtime key-rotation mechanism. Preserve
`SECRET_KEY` across upgrades and restores. There is no credential-editing UI;
provider removal/re-creation deletes linked channel/guide/DVR database entries,
though recorded files remain. Do not present re-creation as lossless key recovery.

---

**Location:** section “Rollback / downgrade hazard”, after the paragraph beginning
“There is no automatic "decrypt everything" downgrade path in this…”. **Bloem adds:**

The legacy downgrade discussion above does not authorize converting Xtream
credentials to plaintext. Older binaries cannot administer protected Xtream sources.
The [September 19 rollback plan](../../../operations/2026-09-19-xtream-deployment.md#upgrade-and-rollback-boundaries)
retains additive schema and ciphertext; it requires a compatibility review if new
providers or recordings have since been created. A rollback to any binary that
predates migration `20261019181724_hash_api_key_verifiers` is also bound by
Silo's *API key verifiers* section: that binary needs the removed `api_key`
column and must not be restarted against the migrated database.

---

**Location:** section “Scope and known gaps”, after the paragraph beginning
“Covered: arr (Requests + Autoscan) API keys, S3 keys, all sensitive…”. **Bloem adds:**

Bloem runs Silo's Jellyfin- and Audiobookshelf-compatible servers as Silo ships
them, so the upstream coverage, including their session tokens and signing key,
applies unchanged; see [protocol compatibility](../../../architecture/bloem-compatibility-removal.md).
Bloem also encrypts Xtream username/password envelopes with tuner/origin binding
and no plaintext fallback (*Bloem Xtream credentials* above).

### Related pages

- [How Live TV works](../../../architecture/bloem-live-tv.md) and
  [Xtream live TV providers](../../../architecture/xtream-live-tv.md).
- [Writing Bloem documentation](../../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../../README.md).
