# Bloem native API by topic

> **Generated** by `scripts/bloem/api-topic-docs.py` from
> [`contracts/api/bloem/v2/openapi.json`](../../../contracts/api/bloem/v2/openapi.json); do not edit
> by hand.

Every typed operation under `/api/bloem/v2`, grouped by the topics of Bloem's explainer pages. The
[wire reference](../../bloem-api-reference.md) adds behavior that does not fit a schema, and the
[API catalogue](../api-catalogue.md) checks the contract against the mounted routes.

| Topic | Operations | Explainer |
| --- | ---: | --- |
| [Live TV](live-tv.md) | 30 | [bloem-live-tv.md](../../architecture/bloem-live-tv.md) |
| [Storage sources](storage.md) | 42 | [bloem-storage-sources.md](../../architecture/bloem-storage-sources.md) |
| [Requests](requests.md) | 33 | [bloem-requests.md](../../architecture/bloem-requests.md) |
| [Plans and preview](plans.md) | 34 | [bloem-plans.md](../../architecture/bloem-plans.md) |
| [Messages and promotions](messages.md) | 39 | [bloem-messages-and-promotions.md](../../architecture/bloem-messages-and-promotions.md) |
| [Apps and clients](apps.md) | 7 | [bloem-apps.md](../../architecture/bloem-apps.md) |
| [Stream insights](insights.md) | 12 | [bloem-stream-insights.md](../../architecture/bloem-stream-insights.md) |
| [Organizations and administration](organizations.md) | 124 | [bloem-organizations.md](../../architecture/bloem-organizations.md) |

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
| `bearerAuth` | A session token or an API key in the Authorization header. The server decides which it holds. An API key is sent as `Authorization: Bearer sa_…`. A profile key (created with profile_id) always acts as its profile: the server supplies X-Profile-Id, and a request naming another profile is 403 permission_denied. An account key chooses the profile per request with X-Profile-Id and needs no X-Profile-Token, even for a PIN-protected profile; GET /api/v2/profiles lists the IDs. Most profile operations require the header and answer 422 validation_failed without it. The few that accept its absence, such as GET /api/v2/profiles and GET /api/v2/user/libraries, then answer for the account as a whole. A key carrying scopes reaches only the operations those scopes admit and is 403 permission_denied everywhere else; GET /api/v2/api-keys/scopes lists them. |
| `bloemAccountSession` | A non-impersonated account access token backed by a login session. API keys and administrative-context tokens are insufficient. Household management authority is checked separately. |
| `bloemAdminLoginSession` | Account access token with a current login session and account incarnation, used only to exchange for an administrative context. API keys without a session are refused. |
| `bloemAdminPushDisplayToken` | A bad_ display token returned by registering an admin app device. It reads that device's own notifications and retires that device, and opens nothing else. |
| `bloemEntitlementAccountsKey` | Bearer sa_ API key owned by an enabled platform administrator, unscoped or carrying admin:entitlements:accounts. Accepted only by the listed catalog/per-account operations. |
| `bloemEntitlementBulkKey` | Bearer sa_ API key owned by an enabled platform administrator, unscoped or carrying admin:entitlements:bulk. Accepted only by authoritative policy reads, snapshots, cohort reads and bulk operations. |
| `bloemLiveTVDeliveryTicket` | Signed Live TV HLS delivery ticket from the session-start URL. Bound to playback ID, user/profile, expiry and purpose=bloem_livetv_hls. Current viewer authorization is re-resolved on every fetch. Does not authorize operations or the MPEG-TS session proxy. |
| `bloemOrganizationContext` | An organization-scoped administrative-context token obtained through /api/bloem/v2/admin/session. The account, originating login session, organization, membership and policy/security revisions are revalidated on every request. An account token or platform-scoped context alone is insufficient. |
| `bloemOrganizationLiveTicket` | A single-use ticket from createBloemOrganizationLiveTicket, offered as the second subprotocol: Sec-WebSocket-Protocol: bloem-live.v1, ticket.<ticket>. Valid for 30 seconds. It is never accepted in a URL. |
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
