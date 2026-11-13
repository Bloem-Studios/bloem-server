# sigstore/rekor v1.5.2, with v1.5.4's PGP package (Bloem compatibility copy)

Silo's release-signature check (#4055) brought in `github.com/sigstore/rekor` v1.5.2
through `sigstore-go`'s test CA, which Silo's `internal/plugins/pluginsig/pluginsigtest`
helper uses. Rekor v1.5.2's `pkg/pki/pgp` imports `golang.org/x/crypto/openpgp`, which
govulncheck reports as GO-2026-5932, so the reachable-vulnerability scan fails. Rekor
v1.5.4 moved that package to `github.com/ProtonMail/go-crypto/openpgp`, but every release
from v1.5.5 on needs Go 1.27, and requiring v1.5.4 directly raises about thirty of Silo's
module requirements.

This directory is rekor v1.5.2 (Apache-2.0, see `LICENSE`), limited to the packages the
build and its tests import, with two changes:

- `pkg/pki/pgp/pgp.go` is rekor v1.5.4's file, unchanged;
- `go.mod` is v1.5.2's, plus `github.com/ProtonMail/go-crypto` v1.4.1 and
  `github.com/cloudflare/circl` v1.6.3 (the first circl release without GO-2026-4550).

The root `go.mod` uses it through `replace github.com/sigstore/rekor v1.5.2 => ...`.
The replace names v1.5.2, so it stops applying as soon as Silo requires another rekor
version. Delete this directory, the replace line and the two indirect requirements it
added once Silo's own fix is merged. Rekor v1.5.4's `pkg/pki/pgp` tests passed against
this copy.

See [Bloem's upstream adapters](../../../docs/architecture/bloem-upstream-adapters.md#rekor-compatibility-copy).
