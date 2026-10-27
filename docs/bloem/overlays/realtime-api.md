# Bloem overlay: Realtime API

**What this overlays.** Silo's [`docs/realtime-api.md`](../../realtime-api.md).
Bloem adds one paragraph to the section “Session-bound socket handshake” and
replaces nothing. An *overlay* is a Bloem-owned page that records Bloem's
additions to, or replacements of, sections of a Silo-owned document. The Silo
file stays exactly as Silo wrote it. See the
[documentation workflow](../documentation-workflow.md).

**Why Bloem differs.** Bloem is multi-tenant, so a realtime socket must stay on
the organization its ticket was issued for. Bloem runs Silo's socket tickets
unchanged and attaches its tenant authority through Silo's optional
`AuthorityBinding` field and validate hook
(`internal/api/handlers/bloem_socket_authority.go`). Since `a7436b4e5`
(2026-10-25), Silo's v1 events, watch-together and playback-control sockets also
run Silo's own handshake. Bloem's earlier v1 audience tickets and their two
ticket routes are removed; see the [v1 scope overlay](architecture/v1-scope.md).
Bloem first documented its realtime difference in the Silo file in September
2026 and moved it here in `47ea7c889` (2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf`. Since the previous review (Silo `6de6d5e5e`), Silo changed the
document once, in `3e2d306b2` (#3141, 2026-10-26): a watch-together room holds
at most 200 suggestions, and frozen v1 suggestion creation shares that cap and a
32 KiB body limit. That applies to Bloem unchanged.

Index: [Bloem documentation](../README.md).

---

**Location:** section “Session-bound socket handshake”, after the paragraph beginning “Native socket/ticket adoption and independent domain review are required before…”. **Bloem adds:**

On Bloem, the events adapter additionally captures the original principal and resolved
tenant in the ticket store's optional server-owned authority binding. Contextless
upgrade and periodic checks revalidate that same account/session/profile, organization,
membership and revisions; they cannot silently select another tenant. Unbound tickets
fail closed. The binding is neither client input nor part of the public ticket response.
One-use consumption, expiry, Origin, PIN, policy fingerprint and revocation checks above
remain in force. See [security invariants](../../architecture/bloem-security-foundation.md#native-seasonal-delivery-and-events-authority)
and [bounded browser evidence](../../architecture/bloem-web-feature-coverage.md#acceptance-evidence-and-limits).
