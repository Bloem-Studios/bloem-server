# Bloem overlay: Cross-platform settings contract

Bloem additions and overrides for the upstream Silo document [`docs/architecture/settings-contract.md`](../../../architecture/settings-contract.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** section “Canonical artifact”, after the paragraph beginning “Clients vendor a pinned copy of the manifest and generate…”. **Bloem adds:**

This repository also owns Go/web bindings and the Kotlin/Swift artifacts under
`contracts/settings/v1/`. Regenerate with `make settings-bindings settings-bindings-native`
and verify with `make verify-settings-bindings-all`. A generator refresh for existing
manifest values does not itself change the manifest revision. The embedded-web checkpoint
refreshed both native artifacts for existing language aliases; server artifact parity
does not prove adoption or release in the Apple/Android repositories.
