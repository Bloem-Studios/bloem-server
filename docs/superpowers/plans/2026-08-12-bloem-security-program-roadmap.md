# Bloem Security Program Roadmap

> **Status:** Superseded. The current program sequence begins with
> `2026-08-13-bloem-opa-tenant-foundation.md` and implements
> `../specs/2026-08-13-bloem-opa-centered-multitenant-authorization-design.md`.

**Source design:** `docs/superpowers/specs/2026-08-12-bloem-multitenant-security-and-authorization-design.md`

The approved design spans independently reviewable security boundaries. It is
delivered as seven ordered plans so every stage has a migration boundary,
behavioral gate, rollback story, and focused security review.

## Delivery order

1. **Tenant and identity foundation**
   - Add organizations, memberships, protected ownership records, profile
     organization identity, and profile access-group assignments.
   - Backfill one default organization without changing `/api/v1` behavior.
   - Add the read-only `/api/v10` capability and membership foundation.
   - Plan: `2026-08-12-bloem-tenant-identity-foundation.md`.

2. **Resource tenancy and platform entitlements**
   - Add organization ownership to libraries, media, tuners, recordings,
     plugins, queues, search documents, caches, events, and object keys.
   - Add platform-owned library/tuner records and organization entitlements.
   - Apply composite foreign keys and PostgreSQL RLS after repository parity is
     green.

3. **Scoped administrative RBAC and OPA**
   - Add capability catalog, role templates, custom roles, scoped assignments,
     delegation ceilings, owner invariants, and typed OPA inputs/decisions.
   - Replace binary admin gates only after generated parity matrices pass.

4. **Authentication, sessions, and API v10**
   - Add organization selection, optional direct-profile credentials, shared
     device enrollment, admin-mode step-up, optional admin MFA policy, service
     identities, session revisions, and stable v10 denial contracts.
   - Preserve complete Silo `/api/v1` login, profile switching, PIN, and legacy
     admin projection behavior.

5. **Adult-title and scene enforcement**
   - Add item/scene sensitivity classification, non-disclosure filters,
     filtered-rendition planning, download/direct-play denial, and authorized
     administrative scopes.

6. **Distributed policy, revocation, support, and audit**
   - Add signed immutable policy snapshots, invalidation, strongly consistent
     suspension checks, stale-state behavior, consent-based support sessions,
     break-glass records, partitioned audit, redaction, and signed export.

7. **Client and production conformance**
   - Publish v1/v10 contracts; add Bloem web and clean-room client flows;
     exercise official-compatible Silo clients; run multi-node revocation,
     cross-tenant, adult non-disclosure, and thousands-of-sessions load gates;
     obtain independent security review before enabling hosted tenancy.

## Program invariants

- `/api/v1` remains a compatibility projection over the same authorization
  services; it never becomes a second enforcer.
- `/api/v10` is additive until a complete capability is advertised.
- Every schema change uses expand/migrate/verify/contract sequencing.
- A feature flag cannot weaken an authorization decision.
- Custom Rego can narrow but never widen vendor decisions.
- Organization isolation does not depend on Rego.
- Existing sessions are never upgraded to stronger authority.
- Each plan ends with a clean-install test, an upgrade test, a rollback test,
  exact Silo compatibility tests, and a security-focused review.
