# Bloem overlay: At-rest credential encryption

This overlay covers Silo's [`docs/architecture/secret-encryption.md`](../../../architecture/secret-encryption.md), which stays exactly as Silo wrote it.

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
A rollback keeps the additive schema and the ciphertext; it requires a
compatibility review if new providers or recordings have been created since. A rollback to any binary that
predates migration `20261019181724_hash_api_key_verifiers` is also bound by
Silo's *API key verifiers* section: that binary needs the removed `api_key`
column and must not be restarted against the migrated database.

---

**Location:** section “Scope and known gaps”, after the paragraph beginning
“Covered: arr (Requests + Autoscan) API keys, S3 keys, all sensitive…”. **Bloem adds:**

Bloem runs Silo's Jellyfin- and Audiobookshelf-compatible servers as Silo ships
them, so the upstream coverage, including their session tokens and signing key,
applies unchanged; see [protocol compatibility](../../../architecture/bloem-protocol-compatibility.md).
Bloem also encrypts Xtream username/password envelopes with tuner/origin binding
and no plaintext fallback (*Bloem Xtream credentials* above).

### Related pages

- [How Live TV works](../../../architecture/bloem-live-tv.md) and
  [Xtream live TV providers](../../../architecture/xtream-live-tv.md).
- [Writing Bloem documentation](../../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../../README.md).
