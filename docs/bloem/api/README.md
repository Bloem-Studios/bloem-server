# Bloem native API by topic

> **Generated** by `scripts/bloem/api-topic-docs.py` from
> [`contracts/api/bloem/v2/openapi.json`](../../../contracts/api/bloem/v2/openapi.json); do not edit
> by hand. The routes Bloem once added to Silo's retired alpha v1 API, which now answer `410 Gone`,
> are recorded on the hand-written [retired v1 routes](v1-routes.md) page.

Every typed operation under `/api/bloem/v2`, grouped by the topics of Bloem's explainer pages. The
[wire reference](../../bloem-api-reference.md) adds behavior that does not fit a schema, and the
[API catalogue](../api-catalogue.md) checks the contract against the mounted routes.

| Topic | Operations | Explainer |
| --- | ---: | --- |
| [Live TV](live-tv.md) | 30 | [bloem-live-tv.md](../../architecture/bloem-live-tv.md) |
| [Storage sources](storage.md) | 42 | [bloem-storage-sources.md](../../architecture/bloem-storage-sources.md) |
| [Requests](requests.md) | 33 | [bloem-requests.md](../../architecture/bloem-requests.md) |
| [Plans and preview](plans.md) | 34 | [bloem-plans.md](../../architecture/bloem-plans.md) |
| [Messages and promotions](messages.md) | 24 | [bloem-messages-and-promotions.md](../../architecture/bloem-messages-and-promotions.md) |
| [Apps and clients](apps.md) | 10 | [bloem-apps.md](../../architecture/bloem-apps.md) |
| [Organizations and administration](organizations.md) | 107 | [bloem-organizations.md](../../architecture/bloem-organizations.md) |
| [Retired v1 routes](v1-routes.md) | hand-written | |

## Conventions

- **Base path.** Every path on these pages is absolute, starting at `/api/bloem/v2`, on the server's
  main address.
- **Profile headers.** Viewer operations act for one household profile: `X-Profile-Id` names it, and
  `X-Profile-Token` carries the proof for a PIN-locked profile. API keys skip PIN verification.
- **Administrative contexts.** Organization and platform administration use a separate token from
  `POST /api/bloem/v2/admin/session` (see [organizations](organizations.md)); an ordinary account
  token does not replace it, and the two contexts are never mixed.
- **Revisions.** Many mutations take the revision the caller last read (for example
  `expected_revision`) and answer `409` when it moved; each operation says so. Read again and review
  before retrying.

## Credentials

| Scheme | Description |
| --- | --- |
| `bearerAuth` | A session token or an API key in the Authorization header. The server decides which it holds. |
| `bloemAccountSession` | A non-impersonated account access token backed by a login session. Direct-profile tokens, API keys and administrative-context tokens cannot manage profile credentials. Household management authority is checked separately. |
| `bloemAdminLoginSession` | Account access token with a current login session and account incarnation, used only to exchange for an administrative context. API keys without a session are refused. |
| `bloemEntitlementAccountsKey` | Bearer sa_ API key owned by an enabled platform administrator, unscoped or carrying admin:entitlements:accounts. Accepted only by the listed catalog/per-account operations. |
| `bloemEntitlementBulkKey` | Bearer sa_ API key owned by an enabled platform administrator, unscoped or carrying admin:entitlements:bulk. Accepted only by authoritative policy reads, snapshots, cohort reads and bulk operations. |
| `bloemLiveTVDeliveryTicket` | Signed Live TV HLS delivery ticket from the session-start URL. Bound to playback ID, user/profile, expiry and purpose=bloem_livetv_hls. Current viewer authorization is re-resolved on every fetch. Does not authorize operations or the MPEG-TS session proxy. |
| `bloemOrganizationContext` | An organization-scoped administrative-context token obtained through /api/bloem/v2/admin/session. The account, originating login session, organization, membership and policy/security revisions are revalidated on every request. An account token or platform-scoped context alone is insufficient. |
| `bloemPlatformContext` | A platform-scoped administrative-context token obtained through /api/bloem/v2/admin/session. The originating login session and current platform authority are revalidated on every request. Account and organization-scoped tokens are insufficient. |

## Errors

### BloemNativeError

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `code` | string |  | Refines error on browse-only preview refusals: preview_only, preview_host, preview_session_invalid or preview_busy. Absent otherwise. |
| `error` | string | yes |  |
| `message` | string | yes |  |

Each operation lists its own error codes under the status that carries them.

## Regenerating

```sh
make bloem-openapi
python3 scripts/bloem/api-topic-docs.py
```

`make verify-bloem-api-catalogue`, which CI runs, fails when a page is out of date.
