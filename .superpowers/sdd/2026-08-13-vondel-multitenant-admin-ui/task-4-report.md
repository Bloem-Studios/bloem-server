# Task 4 report: Organization overview and structured security projections

## Status

Implemented organization-context overview, groups, libraries and entitlements,
invitations, and structured policy-decision explanations. The routes derive the
organization exclusively from the validated administrative context and do not
accept an organization selector.

## Delivered

- Added the organization overview and security projection handlers and mounted
  the exact `/api/v2/admin/organization/*` route surface.
- Reused the tenancy, access-group, resource-tenancy, invitation, and policy
  repositories directly; no v1 HTTP forwarding was introduced.
- Added group deletion impact with transactional profile reassignment counts.
- Distinguished organization-owned libraries from platform-entitled libraries.
- Added organization- and revision-bound entitlement suspension/reactivation
  and revocation.
- Added durable organization identity to invitations and policy decisions,
  including default-organization compatibility backfill and rollback support.
- Kept legacy invitation repository entry points projected to the default
  organization while adding explicit organization-scoped methods.
- Added organization-scoped policy decision list/get methods; foreign IDs are
  non-disclosing `404` responses.
- Added structured explanations containing organization/membership, subject,
  group, tenant library ceiling, action, resource, outcome, reason code, and
  vendor/custom version references. Credential-bearing keys are recursively
  replaced with `[redacted]`; raw input/result samples are never returned.
- Exposed no organization-context policy document create, validation,
  activation, or Rego source routes.

## Controller ruling

The task brief's initial file list did not include a migration or invitation
model/repository changes, but durable invitation isolation is impossible
without persisted organization identity. The controller explicitly approved a
small migration plus additive invitation repository/model extensions, with
existing rows backfilled to the default organization and legacy v1 methods
remaining default-organization compatible. The implementation follows that
ruling and includes up/down/up migration coverage.

## Verification

The following passed against PostgreSQL at `127.0.0.1:5432` using disposable
test databases:

```text
go test ./internal/database ./internal/invitations ./internal/policy \
  ./internal/resourcetenancy ./internal/access \
  -run 'TestOrganizationAdminProjectionMigration|TestRepositoryOrganizationInvitations|TestDecisionRepositoryOrganization|TestStoreListLibraries|TestStoreLibraryEntitlement|TestGroupStoreNeverReadsOrMutatesAnotherOrganization|TestGroupStoreCRUDAndMemberCountsDB' -count=1

go test ./internal/access ./internal/resourcetenancy ./internal/api/handlers \
  -run 'TestV2Organization|TestV2PolicyExplain|TestGroup' -count=1

go test ./internal/api \
  -run 'TestV2AdminOrganizationProjectionRoutes|TestV2AdminPeopleRoutes|TestV2AdminPlatformOrganizationRoutes' -count=1

go test ./internal/policy ./internal/invitations -count=1
go vet ./internal/api/handlers ./internal/access ./internal/resourcetenancy
go vet ./internal/api ./internal/invitations ./internal/models ./internal/policy
git diff --check
```

## Notes

- `/api/v1` route contracts and the platform-only policy document mutation
  boundary are unchanged.
- Organization invitation creation stores the tenant boundary durably and
  returns the one-time claim token only in the creation response; token hashes
  are omitted from list responses.
