package tenancy

// Tenant provisioning (vondel-park growth G2, spec: vondel-park
// docs/superpowers/specs/2026-08-17-vondel-tenant-provisioning-design.md).
//
// A park-sold tenant IS an organization: organizations are the tenancy
// boundary already, so this grafts a quota and an external claim onto the
// existing entity rather than introducing a parallel one. "Slots" bound the
// organization's member accounts (organization_memberships rows);
// "transcodes" is a hard reservation shared across every member. Freezing
// suspends the SAME OrganizationStatus a platform admin's manual suspend
// already uses — the reason (quota vs admin) says why, not a forked
// lifecycle. Enforcement lives at the two natural chokepoints: creating a
// membership checks the slot quota (ProvisionTenantMembership /
// TenantSlotFree / TenantOverQuota), and playback admission checks the
// transcode pool and the frozen flag (TenantLimitsForUser, cached).
//
// Deliberately independent of organizationColumns/scanOrganization and the
// AdminMutationActor-audited mutation path admin_store.go uses for the
// platform admin UI: the park contract is a service-to-service API
// authenticated by admin API key (the same acting-admin gate
// /api/v1/admin/users already uses), not a human, audited admin session.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/entitlements"
)

var (
	// ErrTenantOrganizationNotFound names an unknown or non-tenant organization id.
	ErrTenantOrganizationNotFound = errors.New("tenancy: tenant organization not found")
	// ErrTenantOrganizationInvalid rejects a locally-detectable malformed request.
	ErrTenantOrganizationInvalid = errors.New("tenancy: invalid tenant organization input")
	// ErrTenantSlotsExhausted refuses a member past the tenant's slot quota, or into a frozen tenant.
	ErrTenantSlotsExhausted = errors.New("tenancy: no free tenant slots")
)

// Tenant freeze reasons. A quota freeze lifts itself when usage returns
// under the slot limit; an admin freeze (park's dunning) lifts only on an
// explicit thaw, and a thaw while still over quota re-freezes as quota.
const (
	TenantFrozenReasonQuota = "quota"
	TenantFrozenReasonAdmin = "admin"
)

// TenantOrganization is one park-sold organization plus its live usage.
type TenantOrganization struct {
	ID                         uuid.UUID
	Name                       string
	ExternalOperatorID         string
	ExternalServiceID          string
	Slots                      int
	Transcodes                 int
	SlotsUsed                  int
	Frozen                     bool
	FrozenReason               string
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
	AppliedEntitlementRevision int64
}

// CreateTenantOrganizationInput is the park-facing create request.
type CreateTenantOrganizationInput struct {
	Name                        string
	ExternalOperatorID          string
	ExternalServiceID           string
	Slots                       int
	Transcodes                  int
	EntitlementTemplateKey      string
	EntitlementTemplateRevision int64
}

const tenantOrganizationColumns = `id, name, external_operator_id, external_service_id, slots, transcodes,
	status, suspension_reason, owner_account_id, created_at, updated_at`

func scanTenantOrganization(row rowScanner) (TenantOrganization, error) {
	var (
		t              TenantOrganization
		status         OrganizationStatus
		ownerAccountID *int
	)
	err := row.Scan(&t.ID, &t.Name, &t.ExternalOperatorID, &t.ExternalServiceID, &t.Slots, &t.Transcodes,
		&status, &t.FrozenReason, &ownerAccountID, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return TenantOrganization{}, ErrTenantOrganizationNotFound
	}
	if err != nil {
		return TenantOrganization{}, fmt.Errorf("tenancy: scan tenant organization: %w", err)
	}
	t.Frozen = status == OrganizationSuspended
	return t, nil
}

// tenantSlug derives a stable, pattern-valid slug from a tenant's name and
// its external service claim. Deterministic on the claim so a replayed
// create computes the same slug rather than colliding with the first one.
func tenantSlug(name, externalServiceID string) string {
	var b strings.Builder
	lastHyphen := true // leading hyphens are trimmed by never starting on one
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case !lastHyphen:
			b.WriteRune('-')
			lastHyphen = true
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "tenant"
	}
	if len(base) > 40 {
		base = strings.Trim(base[:40], "-")
	}
	sum := sha256.Sum256([]byte(externalServiceID))
	return "tenant-" + base + "-" + hex.EncodeToString(sum[:])[:8]
}

// CreateTenantOrganization creates a park-sold organization, idempotently on
// ExternalServiceID: a replayed park fulfill job gets the SAME organization
// back, never a second one. The organization has no owner yet — a park
// tenant is sold before any admin account exists for it — so it is created
// 'initializing' per the same rule CreateOrganization enforces for every
// other organization (status 'active' requires an owner). The first admin
// membership ProvisionTenantMembership creates activates it.
func (s *Store) CreateTenantOrganization(ctx context.Context, input CreateTenantOrganizationInput) (TenantOrganization, error) {
	name := strings.TrimSpace(input.Name)
	externalServiceID := strings.TrimSpace(input.ExternalServiceID)
	templateKey := strings.TrimSpace(input.EntitlementTemplateKey)
	if name == "" || externalServiceID == "" || input.Slots < 1 || input.Transcodes < 0 ||
		(templateKey == "") != (input.EntitlementTemplateRevision == 0) || input.EntitlementTemplateRevision < 0 {
		return TenantOrganization{}, ErrTenantOrganizationInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TenantOrganization{}, fmt.Errorf("tenancy: begin tenant organization creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	slug := tenantSlug(name, externalServiceID)
	organization, err := scanTenantOrganization(tx.QueryRow(ctx, `
		INSERT INTO organizations (slug, name, status, external_operator_id, external_service_id, slots, transcodes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (external_service_id) DO UPDATE SET updated_at = organizations.updated_at
		RETURNING `+tenantOrganizationColumns,
		slug, name, OrganizationInitializing, strings.TrimSpace(input.ExternalOperatorID), externalServiceID,
		input.Slots, input.Transcodes))
	if err != nil {
		return TenantOrganization{}, fmt.Errorf("tenancy: create tenant organization: %w", err)
	}
	if templateKey != "" {
		if _, err := entitlements.ApplyTemplateInTx(ctx, tx, organization.ID, templateKey, input.EntitlementTemplateRevision, false); err != nil {
			return TenantOrganization{}, fmt.Errorf("tenancy: apply tenant entitlement template: %w", err)
		}
		organization.AppliedEntitlementRevision = input.EntitlementTemplateRevision
	} else if err := ensureTenantDefaultAccessGroup(ctx, tx, organization.ID); err != nil {
		return TenantOrganization{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantOrganization{}, fmt.Errorf("tenancy: commit tenant organization creation: %w", err)
	}
	return s.withTenantUsage(ctx, organization)
}

// ensureTenantDefaultAccessGroup establishes the same safe fallback policy
// used by the profile access-group migration. Tenant creation and the
// fallback are committed together so the first member can immediately use
// the native profile lifecycle without an unassigned-profile window.
func ensureTenantDefaultAccessGroup(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		WITH candidate AS (
			SELECT id
			FROM access_groups
			WHERE organization_id = $1
			  AND NOT EXISTS (
				SELECT 1 FROM access_groups
				WHERE organization_id = $1 AND is_default
			  )
			ORDER BY id
			LIMIT 1
		)
		UPDATE access_groups
		SET is_default = true, updated_at = now()
		WHERE id = (SELECT id FROM candidate)`, organizationID); err != nil {
		return fmt.Errorf("tenancy: promote tenant default access group: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO access_groups (
			organization_id, name, description, is_default, library_ids,
			max_playback_quality, download_allowed, download_transcode_allowed,
			max_streams, max_transcodes, allowed_permissions, requests_allowed
		)
		SELECT $1, 'Default Group', 'Applied automatically to newly created users.', true, NULL,
		       '', true, false, 5, 5, ARRAY['marker_edit'], true
		WHERE NOT EXISTS (
			SELECT 1 FROM access_groups
			WHERE organization_id = $1
		)`, organizationID); err != nil {
		return fmt.Errorf("tenancy: ensure tenant default access group: %w", err)
	}
	return nil
}

// GetTenantOrganization loads one tenant organization with usage. Refuses a
// real (non-tenant) organization id with the same not-found error an
// unknown id gets — the tenant contract has nothing to say about it.
func (s *Store) GetTenantOrganization(ctx context.Context, id uuid.UUID) (TenantOrganization, error) {
	organization, err := scanTenantOrganization(s.pool.QueryRow(ctx, `
		SELECT `+tenantOrganizationColumns+` FROM organizations
		WHERE id = $1 AND external_service_id IS NOT NULL`, id))
	if err != nil {
		return TenantOrganization{}, err
	}
	return s.withTenantUsage(ctx, organization)
}

// ListTenantOrganizations returns every park-sold organization with usage —
// the reconcile sweep's observed side.
func (s *Store) ListTenantOrganizations(ctx context.Context) ([]TenantOrganization, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.name, o.external_operator_id, o.external_service_id, o.slots, o.transcodes,
		       o.status, o.suspension_reason, o.owner_account_id, o.created_at, o.updated_at,
		       (SELECT count(*) FROM organization_memberships m WHERE m.organization_id = o.id) AS slots_used
		FROM organizations o
		WHERE o.external_service_id IS NOT NULL
		ORDER BY o.created_at, o.id`)
	if err != nil {
		return nil, fmt.Errorf("tenancy: list tenant organizations: %w", err)
	}
	defer rows.Close()
	var out []TenantOrganization
	for rows.Next() {
		var (
			t              TenantOrganization
			status         OrganizationStatus
			ownerAccountID *int
		)
		if err := rows.Scan(&t.ID, &t.Name, &t.ExternalOperatorID, &t.ExternalServiceID, &t.Slots, &t.Transcodes,
			&status, &t.FrozenReason, &ownerAccountID, &t.CreatedAt, &t.UpdatedAt, &t.SlotsUsed); err != nil {
			return nil, fmt.Errorf("tenancy: scan tenant organization list: %w", err)
		}
		t.Frozen = status == OrganizationSuspended
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTenantOrganizationLimits applies a plan change in place. Per the
// park product ruling, a downgrade below the members currently in use
// freezes the organization IMMEDIATELY (reason quota) until members are
// removed; returning under quota lifts a quota freeze automatically — but
// only an admin freeze the same transition would otherwise clear stays put,
// since a plan change is not a thaw.
func (s *Store) UpdateTenantOrganizationLimits(ctx context.Context, id uuid.UUID, slots, transcodes int) (organization TenantOrganization, err error) {
	if slots < 1 || transcodes < 0 {
		return TenantOrganization{}, ErrTenantOrganizationInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TenantOrganization{}, fmt.Errorf("tenancy: begin tenant limits update: %w", err)
	}
	defer rollbackOnError(ctx, tx, &err)

	current, err := s.lockTenantOrganization(ctx, tx, id)
	if err != nil {
		return TenantOrganization{}, err
	}
	used, err := tenantMembershipCount(ctx, tx, id)
	if err != nil {
		return TenantOrganization{}, err
	}
	status, reason := tenantFreezeStateAfterLimitsChange(current, used, slots)

	updated, err := scanTenantOrganization(tx.QueryRow(ctx, `
		UPDATE organizations SET slots = $2, transcodes = $3, status = $4, suspension_reason = $5, updated_at = now()
		WHERE id = $1
		RETURNING `+tenantOrganizationColumns, id, slots, transcodes, status, reason))
	if err != nil {
		return TenantOrganization{}, fmt.Errorf("tenancy: update tenant organization limits: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return TenantOrganization{}, fmt.Errorf("tenancy: commit tenant limits update: %w", err)
	}
	s.invalidateTenantLimitsCache()
	updated.SlotsUsed = used
	return updated, nil
}

// tenantFreezeStateAfterLimitsChange decides the new status/reason for a
// limits change. A downgrade below usage freezes as quota regardless of
// prior state (an admin freeze plus a downgrade is still, at minimum,
// quota-blocked). Returning under quota lifts a QUOTA freeze only — an
// admin freeze is untouched by a plan change, since only an explicit thaw
// may lift it.
func tenantFreezeStateAfterLimitsChange(current tenantOrganizationWithOwner, used, newSlots int) (OrganizationStatus, string) {
	if used > newSlots {
		return OrganizationSuspended, TenantFrozenReasonQuota
	}
	if current.Frozen && current.FrozenReason == TenantFrozenReasonQuota {
		return tenantActiveStatus(current), ""
	}
	if current.Frozen {
		return OrganizationSuspended, current.FrozenReason
	}
	return tenantActiveStatus(current), ""
}

// tenantActiveStatus is 'active' for an organization that already has an
// owner, or 'initializing' — never 'active' without one — for one that does
// not yet, preserving the same invariant CreateOrganization enforces at
// creation.
func tenantActiveStatus(current tenantOrganizationWithOwner) OrganizationStatus {
	if current.ownerAccountID != nil {
		return OrganizationActive
	}
	return OrganizationInitializing
}

// SetTenantOrganizationFrozen freezes (park's dunning suspend, reason
// 'admin') or thaws a tenant organization. A thaw while still over the slot
// quota re-freezes as a quota freeze — the downgrade ruling holds regardless
// of which flag flipped last.
func (s *Store) SetTenantOrganizationFrozen(ctx context.Context, id uuid.UUID, frozen bool) (organization TenantOrganization, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TenantOrganization{}, fmt.Errorf("tenancy: begin tenant freeze: %w", err)
	}
	defer rollbackOnError(ctx, tx, &err)

	current, err := s.lockTenantOrganization(ctx, tx, id)
	if err != nil {
		return TenantOrganization{}, err
	}

	var status OrganizationStatus
	var reason string
	if frozen {
		status, reason = OrganizationSuspended, TenantFrozenReasonAdmin
	} else {
		used, countErr := tenantMembershipCount(ctx, tx, id)
		if countErr != nil {
			return TenantOrganization{}, countErr
		}
		if used > current.Slots {
			status, reason = OrganizationSuspended, TenantFrozenReasonQuota
		} else {
			status, reason = tenantActiveStatus(current), ""
		}
	}

	updated, err := scanTenantOrganization(tx.QueryRow(ctx, `
		UPDATE organizations SET status = $2, suspension_reason = $3, updated_at = now()
		WHERE id = $1
		RETURNING `+tenantOrganizationColumns, id, status, reason))
	if err != nil {
		return TenantOrganization{}, fmt.Errorf("tenancy: set tenant organization frozen: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return TenantOrganization{}, fmt.Errorf("tenancy: commit tenant freeze: %w", err)
	}
	s.invalidateTenantLimitsCache()
	return s.withTenantUsage(ctx, updated)
}

// DeleteTenantOrganization retires a tenant organization. This is a SOFT
// delete, not a row removal — organizations are permanent everywhere else
// in this codebase (the platform admin UI only ever suspends one; nothing
// hard-deletes an organizations row today), and a real one turns out to be
// unsafe here regardless: every organization owns a resource_owners row
// RESTRICT-referenced by media_folders, plugin_installations, entitlements
// and audit history, so a hard delete would refuse the instant a tenant had
// touched any of them.
//
// Clearing external_service_id is what makes the contract's "second delete
// -> 404" true: GetTenantOrganization, the list, and this method itself all
// filter on external_service_id IS NOT NULL, so a retired tenant simply
// stops matching — and because the unique index treats every NULL as
// distinct, the same external_service_id is free for a future create to
// claim again (a canceled-then-re-sold park service becomes a new tenant,
// not a permanent conflict).
//
// The slug gets the same treatment, and for the same reason: tenantSlug is
// deterministic on name and external_service_id, so a second tenant sold
// under the identical name and claim (a genuinely ordinary case — the same
// operator re-buying the same service after canceling it) would otherwise
// collide with the retired row's own slug, which this UPDATE does not
// otherwise touch. Appending the organization's own id, which is already
// unique, guarantees the retired slug can never collide with anything a
// future create computes.
//
// The admin handler calls this BEFORE deleting member accounts through the
// user repository, not after: organizations.owner_account_id is RESTRICT,
// not CASCADE, so deleting the owner's account while the organization still
// names them as owner fails outright. Clearing it here removes that
// ordering trap entirely.
func (s *Store) DeleteTenantOrganization(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE organizations
		SET slug = slug || '-retired-' || left(id::text, 8),
			external_service_id = NULL, external_operator_id = '', slots = NULL, transcodes = NULL,
			owner_account_id = NULL, status = $2, suspension_reason = '', updated_at = now()
		WHERE id = $1 AND external_service_id IS NOT NULL`, id, OrganizationSuspended)
	if err != nil {
		return fmt.Errorf("tenancy: delete tenant organization: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTenantOrganizationNotFound
	}
	s.invalidateTenantLimitsCache()
	return nil
}

// TenantMemberAccountIDs returns the account ids of a tenant organization's
// members, for the admin handler's teardown-before-delete.
func (s *Store) TenantMemberAccountIDs(ctx context.Context, id uuid.UUID) ([]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.account_id FROM organization_memberships m
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.organization_id = $1 AND o.external_service_id IS NOT NULL
		ORDER BY m.account_id`, id)
	if err != nil {
		return nil, fmt.Errorf("tenancy: tenant members: %w", err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var accountID int
		if err := rows.Scan(&accountID); err != nil {
			return nil, err
		}
		ids = append(ids, accountID)
	}
	return ids, rows.Err()
}

// TenantSlotFree reports whether a tenant organization can take one more
// member. Refuses a frozen tenant outright, regardless of slot count — a
// frozen tenant locks members out of new sessions AND new members alike.
func (s *Store) TenantSlotFree(ctx context.Context, id uuid.UUID) error {
	var slots int
	var status OrganizationStatus
	var used int
	err := s.pool.QueryRow(ctx, `
		SELECT o.slots, o.status, (SELECT count(*) FROM organization_memberships m WHERE m.organization_id = o.id)
		FROM organizations o WHERE o.id = $1 AND o.external_service_id IS NOT NULL`, id,
	).Scan(&slots, &status, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTenantOrganizationNotFound
	}
	if err != nil {
		return fmt.Errorf("tenancy: tenant slot check: %w", err)
	}
	if status == OrganizationSuspended || used >= slots {
		return ErrTenantSlotsExhausted
	}
	return nil
}

// TenantOverQuota reports whether a tenant organization now holds more
// members than its slots — the recount half of the create-race gate.
func (s *Store) TenantOverQuota(ctx context.Context, id uuid.UUID) (bool, error) {
	var slots, used int
	err := s.pool.QueryRow(ctx, `
		SELECT o.slots, (SELECT count(*) FROM organization_memberships m WHERE m.organization_id = o.id)
		FROM organizations o WHERE o.id = $1 AND o.external_service_id IS NOT NULL`, id,
	).Scan(&slots, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrTenantOrganizationNotFound
	}
	if err != nil {
		return false, err
	}
	return used > slots, nil
}

// ProvisionTenantMembership creates a membership in a specific tenant
// organization — the admin-user-creation path's counterpart to
// ProvisionDefaultMembership, for a member being provisioned into a park
// tenant rather than this deployment's default organization. The FIRST
// admin membership an 'initializing' tenant organization receives activates
// it (assigns the owner, moves it to 'active') — a tenant is sold before any
// admin account exists for it, so nothing else ever would.
func (s *Store) ProvisionTenantMembership(ctx context.Context, organizationID uuid.UUID, accountID int, legacyRole string) (membership Membership, err error) {
	if legacyRole != legacyRoleAdmin && legacyRole != legacyRoleUser {
		return Membership{}, fmt.Errorf("tenancy: provision tenant membership: invalid legacy role %q", legacyRole)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Membership{}, fmt.Errorf("tenancy: begin tenant membership provisioning: %w", err)
	}
	defer rollbackOnError(ctx, tx, &err)

	organization, err := s.lockTenantOrganization(ctx, tx, organizationID)
	if err != nil {
		return Membership{}, err
	}

	membership, err = scanMembership(tx.QueryRow(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, $3, $4)
		RETURNING id, organization_id, account_id, status, legacy_role, security_revision`,
		organizationID, accountID, MembershipActive, legacyRole))
	if err != nil {
		return Membership{}, fmt.Errorf("tenancy: create tenant membership: %w", err)
	}

	if legacyRole == legacyRoleAdmin && organization.ownerAccountID == nil {
		if _, err = tx.Exec(ctx, `
			UPDATE organizations SET owner_account_id = $2, status = $3, updated_at = now()
			WHERE id = $1`, organizationID, accountID, OrganizationActive); err != nil {
			return Membership{}, fmt.Errorf("tenancy: activate tenant organization owner: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return Membership{}, fmt.Errorf("tenancy: commit tenant membership provisioning: %w", err)
	}
	s.invalidateTenantLimitsCache()
	return membership, nil
}

// lockTenantOrganization loads a tenant organization FOR UPDATE inside tx,
// carrying owner_account_id (not part of the public TenantOrganization
// shape) so callers can decide activation without a second round trip.
func (s *Store) lockTenantOrganization(ctx context.Context, tx pgx.Tx, id uuid.UUID) (tenantOrganizationWithOwner, error) {
	var (
		t      tenantOrganizationWithOwner
		status OrganizationStatus
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, external_operator_id, external_service_id, slots, transcodes,
		       status, suspension_reason, owner_account_id, created_at, updated_at
		FROM organizations WHERE id = $1 AND external_service_id IS NOT NULL FOR UPDATE`, id,
	).Scan(&t.ID, &t.Name, &t.ExternalOperatorID, &t.ExternalServiceID, &t.Slots, &t.Transcodes,
		&status, &t.FrozenReason, &t.ownerAccountID, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenantOrganizationWithOwner{}, ErrTenantOrganizationNotFound
	}
	if err != nil {
		return tenantOrganizationWithOwner{}, fmt.Errorf("tenancy: lock tenant organization: %w", err)
	}
	t.Frozen = status == OrganizationSuspended
	return t, nil
}

// tenantOrganizationWithOwner is TenantOrganization plus the owner id its
// public shape omits — nothing outside this file needs to know whether a
// tenant has an owner yet, only whether it may become 'active'.
type tenantOrganizationWithOwner struct {
	TenantOrganization
	ownerAccountID *int
}

func tenantMembershipCount(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID) (int, error) {
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM organization_memberships WHERE organization_id = $1`, organizationID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("tenancy: tenant membership count: %w", err)
	}
	return count, nil
}

func (s *Store) withTenantUsage(ctx context.Context, organization TenantOrganization) (TenantOrganization, error) {
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM organization_memberships WHERE organization_id = $1`, organization.ID,
	).Scan(&organization.SlotsUsed); err != nil {
		return TenantOrganization{}, fmt.Errorf("tenancy: tenant usage: %w", err)
	}
	return organization, nil
}

// --- Playback admission lookup ---

// TenantUserLimits is the slice of tenant state playback admission needs
// for one user. Zero value (TenantID "") means the user does not belong to
// a park tenant and tenant enforcement does not apply.
type TenantUserLimits struct {
	TenantID      string
	MaxTranscodes int
	Frozen        bool
}

const tenantLimitsCacheTTL = 15 * time.Second

type tenantLimitsCacheEntry struct {
	limits  TenantUserLimits
	expires time.Time
}

var (
	tenantLimitsCacheMu sync.Mutex
	tenantLimitsCache   = map[int]tenantLimitsCacheEntry{}
)

// TenantLimitsForUser answers playback admission's question — which park
// tenant does this account belong to (if any), how many transcodes may that
// tenant's SHARED pool run, and is it frozen — cached for
// tenantLimitsCacheTTL so a freeze or a plan change reaches the stream
// scheduler within the same freshness window the prototype used. An account
// with no tenant organization membership, or one in more than one
// organization (a platform admin who also happens to own a tenant),
// answers the zero value: tenant enforcement is unambiguous only for an
// account whose SOLE organization is a park tenant.
func (s *Store) TenantLimitsForUser(ctx context.Context, accountID int) (TenantUserLimits, error) {
	tenantLimitsCacheMu.Lock()
	if cached, ok := tenantLimitsCache[accountID]; ok && time.Now().Before(cached.expires) {
		limits := cached.limits
		tenantLimitsCacheMu.Unlock()
		return limits, nil
	}
	tenantLimitsCacheMu.Unlock()

	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.transcodes, o.status
		FROM organization_memberships m
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.account_id = $1 AND o.external_service_id IS NOT NULL`, accountID)
	if err != nil {
		return TenantUserLimits{}, fmt.Errorf("tenancy: tenant limits for user: %w", err)
	}
	defer rows.Close()

	var (
		limits TenantUserLimits
		count  int
	)
	for rows.Next() {
		count++
		var (
			id         uuid.UUID
			transcodes int
			status     OrganizationStatus
		)
		if err := rows.Scan(&id, &transcodes, &status); err != nil {
			return TenantUserLimits{}, fmt.Errorf("tenancy: scan tenant limits: %w", err)
		}
		if count == 1 {
			limits = TenantUserLimits{TenantID: id.String(), MaxTranscodes: transcodes, Frozen: status == OrganizationSuspended}
		}
	}
	if err := rows.Err(); err != nil {
		return TenantUserLimits{}, err
	}
	if count != 1 {
		// No tenant, or ambiguous membership in more than one: answer the
		// zero value rather than guess which organization's pool applies.
		limits = TenantUserLimits{}
	}

	tenantLimitsCacheMu.Lock()
	if len(tenantLimitsCache) > 4096 {
		clear(tenantLimitsCache)
	}
	tenantLimitsCache[accountID] = tenantLimitsCacheEntry{limits: limits, expires: time.Now().Add(tenantLimitsCacheTTL)}
	tenantLimitsCacheMu.Unlock()
	return limits, nil
}

// invalidateTenantLimitsCache drops every cached lookup so a freeze, a plan
// change or a membership change reaches admission on the next lookup
// instead of after the TTL.
func (s *Store) invalidateTenantLimitsCache() {
	tenantLimitsCacheMu.Lock()
	clear(tenantLimitsCache)
	tenantLimitsCacheMu.Unlock()
}
