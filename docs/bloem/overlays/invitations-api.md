# Bloem overlay: Email invitation API

Bloem additions and overrides for the upstream Silo document [`docs/invitations-api.md`](../../invitations-api.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../README.md).

**Location:** section “Email invitation API”, after the paragraph beginning “All three POST operations are **non-retryable**, including authentication-refresh…”. **Bloem adds:**

## Bloem organization invitation links

The embedded organization console uses `/api/bloem/v1/admin/organization/invitations`,
with POST creation, POST `/{id}/resend` for confirmed link regeneration, and DELETE
`/{id}` for confirmed revocation. This native flow is limited to user invitations
within the selected organization and checks its policy revision. It returns a one-time
`claim_token` handoff on creation/regeneration and sends **no email**, even when SMTP
is configured. Platform/admin invitations cannot be managed through this surface.

The browser captures the administrative-context generation before a write, reloads
after attempted mutations, and never replays an uncertain create or regeneration.
Handoffs stay out of query/mutation caches and persistent storage; closing the result,
switching context or logging out clears them. See the
[native workflow contract](../../bloem-api-reference.md#embedded-web-workflow-adapters)
for revision, status and audit details. The v2 email-delivery semantics above remain
separate and unchanged.
