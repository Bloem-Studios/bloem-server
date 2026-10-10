# Bloem lifecycle safety

## Tenant retirement

Both keyed lifecycle requests and the legacy tenant-delete path use
`tenancy.MemberService.RetireOrganizationInTransaction`. The transaction locks
the organization before enumerating members, then applies the same deletion
rules as an individual tenant-member removal:

- Remove only the retiring organization's membership for a shared account.
- Preserve accounts with another membership, another organization ownership,
  platform ownership, or an administrator role.
- Delete an otherwise unowned account through the account provisioner.
- Update ownership before deleting an owner account.
- Retire the organization after its memberships have been removed.

Member deletion, ownership changes, command tombstones and organization
retirement commit together. Any member-deletion failure rolls them all back.
The organization row remains as the historical resource owner; retirement
clears its external service identity and suspends it.

After commit, the ordinary member-resource adapter purges only the retiring
organization's resources for surviving accounts. It runs with a bounded context
that survives client cancellation. Cleanup errors are reported after the durable
mutation has committed. A replay returns the completed lifecycle receipt and
does not repeat resource cleanup or mutate a replacement account.

Regression coverage includes shared identities with a second organization's
profile, exclusive-account numeric-ID reuse, retirement rollback after a later
member fails, and both lifecycle and legacy handlers.
