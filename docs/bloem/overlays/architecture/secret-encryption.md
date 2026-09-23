# Bloem overlay: At-rest credential encryption

Bloem additions and overrides for the upstream Silo document [`docs/architecture/secret-encryption.md`](../../../architecture/secret-encryption.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** section “How it works”. **Bloem replaces** the corresponding upstream passage with:

- **Cipher:** AES-256-GCM with a random 12-byte nonce per value. Implemented in
  `internal/secret`.
- **Key derivation:** the data key is HKDF-SHA256–derived from the `SECRET_KEY`
  environment variable with a versioned domain label. `SECRET_KEY` is the
  encryption root; it is never stored in the database.
- **Envelope:** every ciphertext is stored as `enc:v1:<base64url(nonce‖sealed)>`.
  The `enc:v1:` prefix is the version marker (see *Key rotation* below).
- **Row binding (AAD):** each ciphertext is GCM-bound to its logical row via
  additional-authenticated data — `table:column:<pk>` for per-row columns,
  `server_settings:<key>` for settings. A DB-write attacker therefore cannot move
  a credential blob from one row/column to another: decrypting under a different
  binding fails authentication.
- **Legacy integration read path:** an empty value stays empty; a non-`enc:v1:`
  value is treated as legacy plaintext and passed through unchanged (so a not-yet-migrated row keeps
  working); an `enc:v1:` value is decrypted and **any** failure (wrong key,
  tampering, truncation) is surfaced as an error — it is never silently used as a
  credential.

---

**Location:** section “How it works”, after the paragraph beginning “- **Cipher:** AES-256-GCM with a random 12-byte nonce per value.…”. **Bloem adds:**

## Bloem Xtream credentials

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

**Location:** section “Rollback / downgrade hazard”. **Bloem replaces** the corresponding upstream passage with:

Downgrading to a binary that predates the original at-rest encryption support is
**not** safe while secrets are encrypted, because the old binary has no read path:
it would read
`enc:v1:auth.jwt_secret` as a literal JWT secret (invalidating all sessions) and
read `enc:v1:`-prefixed integration keys as garbage credentials. It would also
pass the reserved plugin-config envelope object to plugins instead of their
configuration.

---

**Location:** section “Rollback / downgrade hazard”, after the paragraph beginning “There is no automatic "decrypt everything" downgrade path in this…”. **Bloem adds:**

The legacy downgrade discussion above does not authorize converting Xtream
credentials to plaintext. Older binaries cannot administer protected Xtream sources.
The [September 19 rollback plan](../../../operations/2026-09-19-xtream-deployment.md#upgrade-and-rollback-boundaries)
retains additive schema and ciphertext; it requires a compatibility review if new
providers or recordings have since been created.

---

**Location:** section “Scope and known gaps”. **Bloem replaces** the corresponding upstream passage with:

Covered: arr (Requests + Autoscan) API keys, S3 keys, all sensitive
`server_settings`, watch-sync tokens, webhook-sync `access_token`,
history-import admin/session tokens and temporary server-list credentials,
subtitle provider `api_key`/`password`,
the Jellyfin-compat session's bridged Silo access/refresh tokens
(`jellycompat_sessions.streamapp_access_token` / `streamapp_refresh_token`), and
the ABS signing key. Bloem also encrypts Xtream username/password envelopes with
tuner/origin binding and no plaintext fallback. Plugin runtime configuration is
encrypted as one opaque row-bound envelope rather than by manifest field. Consequently, runtime code
must use `RuntimeConfigStore`; database JSON-member queries and indexes are not
supported for `plugin_runtime_configs.config_value`.
