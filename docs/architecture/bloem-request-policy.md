# Bloem request policy

The movie and series request service remains shared with Silo. Bloem installs
owned tenant adapters before composing either native listener, independently
of lifecycle-idempotency configuration. Unsupported production dependencies
fail composition instead of silently retaining account-wide authorization.
Database-free composition and ordinary Silo services retain their existing
behavior.

## Viewer requests

Permission and group quota resolve from the request's verified access scope.
The account, organization, membership and profile must agree with that scope.
A single database snapshot checks the enabled account, active membership,
organization state, security and policy revisions, and profile ownership. It
then reads permission and quota from that profile's organization-owned group.
The account repository's default or oldest membership does not select a
profile's request policy.

Missing, foreign or unverified profiles fail closed. Profile-free compatibility
resolution is restricted to the actual default organization in the legacy
context, including its supported initializing state. An asserted default flag
cannot turn a hosted organization into that compatibility context.

The shared service still owns permission overrides, settings/group/user-limit
precedence, approval rules, quota consumption and request enrichment. Account
consumption remains shared across profiles; selecting a different profile does
not reset its counter. Services without the Bloem provider keep Silo's ordinary
account-group resolution.

## Administrative reads and group writes

`GET /api/v2/admin/request-users/{user_id}/usage` explicitly addresses an
account and supplies no target profile. Its separate adapter reports that
account's current membership policy in the actor's organization. It does not
infer a profile or relax the viewer-request resolver. One live snapshot checks
the enabled actor's current administrator membership and tenant revisions,
then the enabled target's active membership and organization-owned group.
Foreign, suspended and disabled targets are refused. Quota evaluation reuses
the shared settings, overrides and account-consumption calculation.

Administrative group-limit reads and conditional writes bind the group to the
verified request organization and actor. A write locks the group's ownership
through the revision-checked transaction. A preflight against another
organization's group cannot authorize its quota or approval policy. These
adapters retain the request repository's other optional capabilities.

Regression coverage includes conflicting account/profile groups, sibling
profile policies, membership and user-limit overrides, account consumption,
internal request enrichment, foreign groups, stale authority, production
listener composition, real v2 requests, and ordinary Silo compatibility.
