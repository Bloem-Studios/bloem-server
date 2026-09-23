# Bloem overlay: Invitations and server-driven onboarding

Bloem additions and overrides for the upstream Silo document [`docs/architecture/invitations-onboarding.md`](../../../architecture/invitations-onboarding.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** section “Invitations and server-driven onboarding”. **Bloem replaces** the corresponding upstream passage with:

Two complementary entry paths coexist: shareable multi-use `invite_codes`
("drop a code in Discord") and personal, emailed invitations — a single-use
capability token bound to one email address, carrying the access decisions the
admin made at send time. Invitations live in `internal/invitations`; the
first-run tour lives in `internal/onboarding`. Bloem's organization console also
creates email-bound invitations with manually delivered links; that native flow
never sends mail. See [organization invitation links](../invitations-api.md#bloem-organization-invitation-links).

---

**Location:** section “Token model”. **Bloem replaces** the corresponding upstream passage with:

Token handling mirrors the email-verification flow: 32 random bytes,
`base64.RawURLEncoding` in the claim URL (`/invite/<token>`), SHA-256 hex
stored at rest as `token_hash`. The raw token exists in the sent email (or in
the create response when mail is unconfigured, or the native organization's one-time
create/regenerate handoff) and nowhere else — a database
dump yields no usable links.

---

**Location:** section “Lifecycle invariants”. **Bloem replaces** the corresponding upstream passage with:

- **One live invitation per address.** A partial unique index
  (`invitations_one_pending_idx`, on `email` where neither accepted nor
  revoked) enforces it; `Repository.Create` revokes any live invitation for
  the address in the same transaction. Re-invite and resend therefore
  *supersede*: a forwarded copy of the old link stops working.
- **Accept commits account and claim together.** The invitation repository
  locks the token row and provisions the account and requested PostgreSQL
  profile through the same transaction. The final claim checks wall-clock
  expiry; expiry or any provisioning/claim failure rolls all three back.
  Revoke and replacement serialize on that row: if they win first, acceptance
  creates nothing; if acceptance wins first, later revocation does not delete
  the account. Resend accepts only the requested pending or expired source;
  it cannot revive revoked or accepted history or supersede a newer link from
  a stale request.
- **Profile storage must support the transaction.** Required default profiles
  use the PostgreSQL provider's transaction capability, preserved through the
  notification wrapper. A SQLite profile store cannot join that transaction:
  acceptance with `create_profile=true` fails before account/membership insertion or
  user-store lookup, so it also creates no SQLite files. Provider preflight and the
  returned store's transactional writer are both checked.
  `create_profile=false` remains supported. This restriction applies to emailed
  invitations; ordinary signup invite-code behavior is unchanged.
- **Login follows acceptance.** A session-issuance failure leaves the committed
  account and accepted invitation intact. The domain returns that account with
  `ErrSessionStart`; the invitee can use ordinary sign-in with the chosen
  password. A lost commit response is uncertain, not proof of rollback. Neither
  failure authorizes automatic replay or compensating account deletion.
- **Revoke is idempotent.** The administrator DELETE route revokes the link;
  physical history deletion is a separate repository operation.
- **Mail degrades loudly, not silently.** Claim links resolve their base from
  the server public URL;
  with neither set, creation fails. With no mail sender configured, the row is
  still created and the claim URL returned with `EmailSent` false so the admin
  copies the link instead of believing an email went out.
  SMTP runs after storage commits. A delivery error retains the new invitation
  and invalidates the old link; the domain returns the committed `SendResult`
  alongside the error. `EmailSent=false` with an error means failed or uncertain
  delivery, not proof that no message arrived. Repeating send/resend creates a
  new token and may send another email; there is no durable replay receipt or
  exactly-once delivery guarantee. The current legacy HTTP error response does
  not expose this partial result; a later transport must represent it explicitly.

---

**Location:** section “Accounts vs household profiles”. **Bloem replaces** the corresponding upstream passage with:

Invitations create **login accounts** (`users` rows), not profiles. Several
household profiles share one account's `user_id`; a profile's `is_primary`
marks the household parent, which is distinct from the server-wide `admin`
role on the account. The invitation's `create_profile` flag controls whether
accept provisions a default profile (named from the email's local part) for
clients that do not render a household-setup step; profiles added afterwards
use the ordinary profiles API. Someone needing a separate login account receives a
second invitation. Optional direct-profile credentials remain account-owned and only
work on their supported routes; they do not create another account or enable browser
direct-profile login. A profile's rating
ceiling and library restrictions layer *under* the account's invitation-bound
access; a profile can never see more than the account received.
