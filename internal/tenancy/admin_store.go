package tenancy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	defaultAdminPageLimit = 50
	maximumAdminPageLimit = 200
)

var (
	ErrAuthorizationStateChanged = errors.New("authorization state changed")
	ErrOrganizationSlugConflict  = errors.New("organization slug conflicts with an existing organization")
	ErrInvalidCursor             = errors.New("invalid cursor")
	ErrInvalidAdminMutation      = errors.New("invalid administrative mutation")
)

type OrganizationCursor struct {
	Name string    `json:"name"`
	ID   uuid.UUID `json:"id"`
}

type OrganizationSummary struct {
	Organization
	MembershipCount  int64 `json:"membership_count"`
	ProfileCount     int64 `json:"profile_count"`
	LibraryCount     int64 `json:"library_count"`
	EntitlementCount int64 `json:"entitlement_count"`
}

type OrganizationPage struct {
	Items      []OrganizationSummary `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

type OrganizationFilter struct {
	Query  string
	Status *OrganizationStatus
	Limit  int
	Cursor string
}

type CreateOrganizationInput struct {
	Name           string
	Slug           string
	OwnerAccountID int
}

type UpdateOrganizationInput struct {
	Name *string
	Slug *string
}

type MembershipSummary struct {
	Membership
	Email    string `json:"email"`
	Username string `json:"username"`
}

type MembershipPage struct {
	Items      []MembershipSummary `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

type MembershipFilter struct {
	Limit  int
	Cursor string
}

type CreateMembershipInput struct {
	AccountID  int
	LegacyRole string
	Status     MembershipStatus
}

type UpdateMembershipInput struct {
	LegacyRole *string
	Status     *MembershipStatus
}

type membershipCursor struct {
	AccountID int       `json:"account_id"`
	ID        uuid.UUID `json:"id"`
}

// GetOrganizationSummary returns one organization with exact, tenant-bound
// directory counts without loading its related collections.
func (s *Store) GetOrganizationSummary(ctx context.Context, organizationID uuid.UUID) (OrganizationSummary, error) {
	var item OrganizationSummary
	err := s.pool.QueryRow(ctx, `
		SELECT o.id, o.slug, o.name, o.status, o.owner_account_id, o.policy_revision, o.is_default,
		       (SELECT count(*) FROM organization_memberships m WHERE m.organization_id = o.id),
		       (SELECT count(*) FROM user_profiles p WHERE p.organization_id = o.id),
		       (SELECT count(*) FROM media_folders f JOIN resource_owners ro ON ro.id = f.owner_id WHERE ro.organization_id = o.id),
		       (SELECT count(*) FROM organization_entitlements e WHERE e.organization_id = o.id AND e.status <> 'revoked')
		FROM organizations o
		WHERE o.id = $1`, organizationID).Scan(
		&item.ID, &item.Slug, &item.Name, &item.Status, &item.OwnerAccountID, &item.PolicyRevision, &item.Default,
		&item.MembershipCount, &item.ProfileCount, &item.LibraryCount, &item.EntitlementCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationSummary{}, ErrOrganizationNotFound
	}
	if err != nil {
		return OrganizationSummary{}, fmt.Errorf("load organization summary: %w", err)
	}
	return item, nil
}

func (s *Store) ListOrganizations(ctx context.Context, filter OrganizationFilter) (OrganizationPage, error) {
	limit := boundedAdminLimit(filter.Limit)
	conditions := []string{"true"}
	args := make([]any, 0, 6)
	if query := strings.TrimSpace(filter.Query); query != "" {
		args = append(args, "%"+strings.ToLower(query)+"%")
		conditions = append(conditions, fmt.Sprintf("(lower(o.name) LIKE $%d OR lower(o.slug) LIKE $%d)", len(args), len(args)))
	}
	if filter.Status != nil {
		if !validOrganizationStatus(*filter.Status) {
			return OrganizationPage{}, ErrInvalidAdminMutation
		}
		args = append(args, *filter.Status)
		conditions = append(conditions, fmt.Sprintf("o.status = $%d", len(args)))
	}
	if filter.Cursor != "" {
		cursor, err := decodeOrganizationCursor(filter.Cursor)
		if err != nil {
			return OrganizationPage{}, err
		}
		args = append(args, strings.ToLower(cursor.Name), cursor.ID)
		conditions = append(conditions, fmt.Sprintf("(lower(o.name), o.id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.slug, o.name, o.status, o.owner_account_id, o.policy_revision, o.is_default,
		       (SELECT count(*) FROM organization_memberships m WHERE m.organization_id = o.id),
		       (SELECT count(*) FROM user_profiles p WHERE p.organization_id = o.id),
		       (SELECT count(*) FROM media_folders f JOIN resource_owners ro ON ro.id = f.owner_id WHERE ro.organization_id = o.id),
		       (SELECT count(*) FROM organization_entitlements e WHERE e.organization_id = o.id AND e.status <> 'revoked')
		FROM organizations o
		WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY lower(o.name), o.id
		LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return OrganizationPage{}, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()

	items := make([]OrganizationSummary, 0, limit+1)
	for rows.Next() {
		var item OrganizationSummary
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Status, &item.OwnerAccountID, &item.PolicyRevision, &item.Default,
			&item.MembershipCount, &item.ProfileCount, &item.LibraryCount, &item.EntitlementCount); err != nil {
			return OrganizationPage{}, fmt.Errorf("scan organization summary: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return OrganizationPage{}, fmt.Errorf("iterate organizations: %w", err)
	}
	page := OrganizationPage{Items: items}
	if len(items) > limit {
		last := items[limit-1]
		page.NextCursor = encodeAdminCursor(OrganizationCursor{Name: last.Name, ID: last.ID})
		page.Items = items[:limit]
	}
	return page, nil
}

func (s *Store) CreateOrganization(ctx context.Context, input CreateOrganizationInput) (organization Organization, err error) {
	name, slug := strings.TrimSpace(input.Name), strings.ToLower(strings.TrimSpace(input.Slug))
	if name == "" || slug == "" || input.OwnerAccountID <= 0 {
		return Organization{}, ErrInvalidAdminMutation
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, fmt.Errorf("begin organization creation: %w", err)
	}
	defer rollbackOnError(ctx, tx, &err)

	var enabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM users WHERE id = $1 FOR UPDATE`, input.OwnerAccountID).Scan(&enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Organization{}, ErrOwnerNotEligible
		}
		return Organization{}, fmt.Errorf("load organization owner: %w", err)
	}
	if !enabled {
		return Organization{}, ErrOwnerNotEligible
	}
	organization, err = scanOrganization(tx.QueryRow(ctx, `
		INSERT INTO organizations (slug, name, status, owner_account_id)
		VALUES ($1, $2, $3, $4)
		RETURNING `+organizationColumns, slug, name, OrganizationActive, input.OwnerAccountID))
	if err != nil {
		return Organization{}, mapAdminWriteError("create organization", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, $3, $4)`, organization.ID, input.OwnerAccountID, MembershipActive, legacyRoleAdmin); err != nil {
		return Organization{}, mapAdminWriteError("create owner membership", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Organization{}, fmt.Errorf("commit organization creation: %w", err)
	}
	return organization, nil
}

func (s *Store) UpdateOrganization(ctx context.Context, organizationID uuid.UUID, expectedRevision int64, input UpdateOrganizationInput) (Organization, error) {
	if organizationID == uuid.Nil || expectedRevision <= 0 || (input.Name == nil && input.Slug == nil) {
		return Organization{}, ErrInvalidAdminMutation
	}
	var name, slug *string
	if input.Name != nil {
		trimmed := strings.TrimSpace(*input.Name)
		if trimmed == "" {
			return Organization{}, ErrInvalidAdminMutation
		}
		name = &trimmed
	}
	if input.Slug != nil {
		trimmed := strings.ToLower(strings.TrimSpace(*input.Slug))
		if trimmed == "" {
			return Organization{}, ErrInvalidAdminMutation
		}
		slug = &trimmed
	}
	organization, err := scanOrganization(s.pool.QueryRow(ctx, `
		UPDATE organizations
		SET name = COALESCE($3, name), slug = COALESCE($4, slug),
		    policy_revision = policy_revision + 1, updated_at = now()
		WHERE id = $1 AND policy_revision = $2
		RETURNING `+organizationColumns, organizationID, expectedRevision, name, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, s.organizationMutationMiss(ctx, organizationID)
	}
	if err != nil {
		return Organization{}, mapAdminWriteError("update organization", err)
	}
	return organization, nil
}

func (s *Store) SetOrganizationStatus(ctx context.Context, organizationID uuid.UUID, expectedRevision int64, status OrganizationStatus) (Organization, error) {
	if organizationID == uuid.Nil || expectedRevision <= 0 || (status != OrganizationActive && status != OrganizationSuspended) {
		return Organization{}, ErrInvalidAdminMutation
	}
	organization, err := scanOrganization(s.pool.QueryRow(ctx, `
		UPDATE organizations
		SET status = $3, policy_revision = policy_revision + 1, updated_at = now()
		WHERE id = $1 AND policy_revision = $2
		RETURNING `+organizationColumns, organizationID, expectedRevision, status))
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, s.organizationMutationMiss(ctx, organizationID)
	}
	if err != nil {
		return Organization{}, mapAdminWriteError("set organization status", err)
	}
	return organization, nil
}

func (s *Store) TransferOwnership(ctx context.Context, organizationID uuid.UUID, expectedRevision int64, accountID int) (organization Organization, err error) {
	if organizationID == uuid.Nil || expectedRevision <= 0 || accountID <= 0 {
		return Organization{}, ErrInvalidAdminMutation
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, fmt.Errorf("begin ownership transfer: %w", err)
	}
	defer rollbackOnError(ctx, tx, &err)

	organization, err = scanOrganization(tx.QueryRow(ctx, `SELECT `+organizationColumns+` FROM organizations WHERE id = $1 FOR UPDATE`, organizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrOrganizationNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("lock organization for ownership transfer: %w", err)
	}
	if organization.PolicyRevision != expectedRevision {
		return Organization{}, ErrAuthorizationStateChanged
	}
	membership, err := scanMembership(tx.QueryRow(ctx, `
		SELECT id, organization_id, account_id, status, legacy_role, security_revision
		FROM organization_memberships
		WHERE organization_id = $1 AND account_id = $2
		FOR UPDATE`, organizationID, accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrMembershipNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("lock new owner membership: %w", err)
	}
	if membership.Status != MembershipActive {
		return Organization{}, ErrOwnerNotEligible
	}
	if _, err = tx.Exec(ctx, `
		UPDATE organization_memberships
		SET legacy_role = $2, security_revision = security_revision + 1, updated_at = now()
		WHERE id = $1`, membership.ID, legacyRoleAdmin); err != nil {
		return Organization{}, mapAdminWriteError("promote new owner membership", err)
	}
	organization, err = scanOrganization(tx.QueryRow(ctx, `
		UPDATE organizations
		SET owner_account_id = $2, policy_revision = policy_revision + 1, updated_at = now()
		WHERE id = $1
		RETURNING `+organizationColumns, organizationID, accountID))
	if err != nil {
		return Organization{}, mapAdminWriteError("transfer organization ownership", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Organization{}, fmt.Errorf("commit ownership transfer: %w", err)
	}
	return organization, nil
}

func (s *Store) ListOrganizationMemberships(ctx context.Context, organizationID uuid.UUID, filter MembershipFilter) (MembershipPage, error) {
	if organizationID == uuid.Nil {
		return MembershipPage{}, ErrOrganizationNotFound
	}
	limit := boundedAdminLimit(filter.Limit)
	args := []any{organizationID}
	condition := ""
	if filter.Cursor != "" {
		var cursor membershipCursor
		if err := decodeAdminCursor(filter.Cursor, &cursor); err != nil || cursor.AccountID <= 0 || cursor.ID == uuid.Nil {
			return MembershipPage{}, ErrInvalidCursor
		}
		args = append(args, cursor.AccountID, cursor.ID)
		condition = " AND (m.account_id, m.id) > ($2, $3)"
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, `
		SELECT m.id, m.organization_id, m.account_id, m.status, m.legacy_role, m.security_revision,
		       u.email, u.username
		FROM organization_memberships m
		JOIN users u ON u.id = m.account_id
		WHERE m.organization_id = $1`+condition+`
		ORDER BY m.account_id, m.id
		LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return MembershipPage{}, fmt.Errorf("list organization memberships: %w", err)
	}
	defer rows.Close()
	items := make([]MembershipSummary, 0, limit+1)
	for rows.Next() {
		var item MembershipSummary
		if err := rows.Scan(&item.ID, &item.OrganizationID, &item.AccountID, &item.Status, &item.LegacyRole, &item.SecurityRevision, &item.Email, &item.Username); err != nil {
			return MembershipPage{}, fmt.Errorf("scan organization membership: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return MembershipPage{}, fmt.Errorf("iterate organization memberships: %w", err)
	}
	if len(items) == 0 {
		if _, err := s.GetOrganization(ctx, organizationID); err != nil {
			return MembershipPage{}, err
		}
	}
	page := MembershipPage{Items: items}
	if len(items) > limit {
		last := items[limit-1]
		page.NextCursor = encodeAdminCursor(membershipCursor{AccountID: last.AccountID, ID: last.ID})
		page.Items = items[:limit]
	}
	return page, nil
}

// GetOrganizationMembership reads a membership only through its containing
// organization, so a membership identifier from another tenant is hidden.
func (s *Store) GetOrganizationMembership(ctx context.Context, organizationID, membershipID uuid.UUID) (Membership, error) {
	membership, err := scanMembership(s.pool.QueryRow(ctx, `
		SELECT id, organization_id, account_id, status, legacy_role, security_revision
		FROM organization_memberships
		WHERE organization_id = $1 AND id = $2`, organizationID, membershipID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrMembershipNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("load organization membership: %w", err)
	}
	return membership, nil
}

func (s *Store) CreateMembership(ctx context.Context, organizationID uuid.UUID, expectedRevision int64, input CreateMembershipInput) (membership Membership, organization Organization, err error) {
	if organizationID == uuid.Nil || expectedRevision <= 0 || input.AccountID <= 0 || !validLegacyRole(input.LegacyRole) {
		return Membership{}, Organization{}, ErrInvalidAdminMutation
	}
	if input.Status == "" {
		input.Status = MembershipActive
	}
	if !validMembershipStatus(input.Status) {
		return Membership{}, Organization{}, ErrInvalidAdminMutation
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Membership{}, Organization{}, fmt.Errorf("begin membership creation: %w", err)
	}
	defer rollbackOnError(ctx, tx, &err)
	organization, err = scanOrganization(tx.QueryRow(ctx, `SELECT `+organizationColumns+` FROM organizations WHERE id = $1 FOR UPDATE`, organizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, Organization{}, ErrOrganizationNotFound
	}
	if err != nil {
		return Membership{}, Organization{}, fmt.Errorf("lock organization for membership creation: %w", err)
	}
	if organization.PolicyRevision != expectedRevision {
		return Membership{}, Organization{}, ErrAuthorizationStateChanged
	}
	membership, err = scanMembership(tx.QueryRow(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, $3, $4)
		RETURNING id, organization_id, account_id, status, legacy_role, security_revision`, organizationID, input.AccountID, input.Status, input.LegacyRole))
	if err != nil {
		return Membership{}, Organization{}, mapAdminWriteError("create membership", err)
	}
	organization, err = scanOrganization(tx.QueryRow(ctx, `
		UPDATE organizations SET policy_revision = policy_revision + 1, updated_at = now()
		WHERE id = $1 RETURNING `+organizationColumns, organizationID))
	if err != nil {
		return Membership{}, Organization{}, mapAdminWriteError("advance organization membership revision", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Membership{}, Organization{}, fmt.Errorf("commit membership creation: %w", err)
	}
	return membership, organization, nil
}

func (s *Store) UpdateMembership(ctx context.Context, organizationID, membershipID uuid.UUID, expectedRevision int64, input UpdateMembershipInput) (membership Membership, organization Organization, err error) {
	if organizationID == uuid.Nil || membershipID == uuid.Nil || expectedRevision <= 0 || (input.LegacyRole == nil && input.Status == nil) {
		return Membership{}, Organization{}, ErrInvalidAdminMutation
	}
	if input.LegacyRole != nil && !validLegacyRole(*input.LegacyRole) {
		return Membership{}, Organization{}, ErrInvalidAdminMutation
	}
	if input.Status != nil && !validMembershipStatus(*input.Status) {
		return Membership{}, Organization{}, ErrInvalidAdminMutation
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Membership{}, Organization{}, fmt.Errorf("begin membership update: %w", err)
	}
	defer rollbackOnError(ctx, tx, &err)
	organization, err = scanOrganization(tx.QueryRow(ctx, `SELECT `+organizationColumns+` FROM organizations WHERE id = $1 FOR UPDATE`, organizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, Organization{}, ErrOrganizationNotFound
	}
	if err != nil {
		return Membership{}, Organization{}, fmt.Errorf("lock organization for membership update: %w", err)
	}
	membership, err = scanMembership(tx.QueryRow(ctx, `
		SELECT id, organization_id, account_id, status, legacy_role, security_revision
		FROM organization_memberships WHERE organization_id = $1 AND id = $2 FOR UPDATE`, organizationID, membershipID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, Organization{}, ErrMembershipNotFound
	}
	if err != nil {
		return Membership{}, Organization{}, fmt.Errorf("lock membership for update: %w", err)
	}
	if membership.SecurityRevision != expectedRevision {
		return Membership{}, Organization{}, ErrAuthorizationStateChanged
	}
	if organization.OwnerAccountID != nil && *organization.OwnerAccountID == membership.AccountID {
		if (input.Status != nil && *input.Status != MembershipActive) || (input.LegacyRole != nil && *input.LegacyRole != legacyRoleAdmin) {
			return Membership{}, Organization{}, ErrMembershipConflict
		}
	}
	membership, err = scanMembership(tx.QueryRow(ctx, `
		UPDATE organization_memberships
		SET legacy_role = COALESCE($2, legacy_role), status = COALESCE($3, status),
		    security_revision = security_revision + 1, updated_at = now()
		WHERE id = $1
		RETURNING id, organization_id, account_id, status, legacy_role, security_revision`, membershipID, input.LegacyRole, input.Status))
	if err != nil {
		return Membership{}, Organization{}, mapAdminWriteError("update membership", err)
	}
	organization, err = scanOrganization(tx.QueryRow(ctx, `
		UPDATE organizations SET policy_revision = policy_revision + 1, updated_at = now()
		WHERE id = $1 RETURNING `+organizationColumns, organizationID))
	if err != nil {
		return Membership{}, Organization{}, mapAdminWriteError("advance organization membership revision", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Membership{}, Organization{}, fmt.Errorf("commit membership update: %w", err)
	}
	return membership, organization, nil
}

func boundedAdminLimit(limit int) int {
	if limit <= 0 {
		return defaultAdminPageLimit
	}
	if limit > maximumAdminPageLimit {
		return maximumAdminPageLimit
	}
	return limit
}

func validOrganizationStatus(status OrganizationStatus) bool {
	return status == OrganizationInitializing || status == OrganizationActive || status == OrganizationSuspended
}

func validMembershipStatus(status MembershipStatus) bool {
	return status == MembershipInvited || status == MembershipActive || status == MembershipSuspended
}

func validLegacyRole(role string) bool { return role == legacyRoleAdmin || role == legacyRoleUser }

func encodeAdminCursor(value any) string {
	raw, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeAdminCursor(cursor string, value any) error {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || json.Unmarshal(raw, value) != nil {
		return ErrInvalidCursor
	}
	return nil
}

func decodeOrganizationCursor(cursor string) (OrganizationCursor, error) {
	var value OrganizationCursor
	if err := decodeAdminCursor(cursor, &value); err != nil || value.Name == "" || value.ID == uuid.Nil {
		return OrganizationCursor{}, ErrInvalidCursor
	}
	return value, nil
}

func (s *Store) organizationMutationMiss(ctx context.Context, organizationID uuid.UUID) error {
	var present bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE id = $1)`, organizationID).Scan(&present); err != nil {
		return fmt.Errorf("inspect organization mutation conflict: %w", err)
	}
	if !present {
		return ErrOrganizationNotFound
	}
	return ErrAuthorizationStateChanged
}

func rollbackOnError(ctx context.Context, tx pgx.Tx, err *error) {
	if *err != nil {
		_ = tx.Rollback(ctx)
	}
}

func mapAdminWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			if pgErr.ConstraintName == "organizations_slug_ci_idx" {
				return ErrOrganizationSlugConflict
			}
			return ErrMembershipConflict
		case "23503":
			return ErrOwnerNotEligible
		case "23514", "23502", "22P02":
			return ErrInvalidAdminMutation
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
