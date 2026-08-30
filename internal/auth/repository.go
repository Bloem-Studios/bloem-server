package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
)

// Sentinel errors for repository operations.
var (
	ErrNotFound  = errors.New("user not found")
	ErrDuplicate = errors.New("duplicate user")
)

// IsNotFound returns true if the error is a "not found" error.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// IsDuplicate returns true if the error is a "duplicate" error.
func IsDuplicate(err error) bool {
	return errors.Is(err, ErrDuplicate)
}

// CheckPassword verifies a plaintext password against the user's bcrypt hash.
// This is a standalone function, not a repository method.
func CheckPassword(user *models.User, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	return err == nil
}

// UserRepository provides CRUD operations for the users table.
type UserRepository struct {
	pool *pgxpool.Pool
}

// NewUserRepository creates a new UserRepository backed by the given pool.
func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// allColumns is the list of columns returned by all SELECT queries.
// Kept in one place so scanUser stays in sync.
//
// Identity stays on users; the policy columns were moved to
// organization_memberships by 20260829085838_membership_policy_isolation and are
// read back through userSource. models.User is an account-shaped view, so it
// projects the membership in the DEFAULT organization -- which is the same row
// these columns held before the handoff for any deployment with one
// organization. Callers that need a specific tenant's policy (adminpeople,
// entitlements) already query organization_memberships directly and do not come
// through here.
//
// COALESCE mirrors the pre-handoff nullability: an account with no default-org
// membership reads as the unset policy it used to have on users.
const allColumns = `u.id, u.account_incarnation_id, u.email, u.username, u.password_hash, u.local_password_login_enabled, u.role, COALESCE(m.permissions, '{}'), u.enabled,
	m.library_ids, m.max_playback_quality, COALESCE(m.access_policy_revision, 1),
	m.max_streams, m.max_transcodes, m.transcode_allowed, m.audio_transcode_allowed, COALESCE(m.max_profiles, 5), m.download_allowed,
	m.download_transcode_allowed, m.requests_allowed, m.access_group_id, u.created_at, u.updated_at`

// userSource joins an account to the membership that represents it.
//
// models.User is account-shaped while policy is per-membership, so one row has
// to stand for the account. The default organization wins when the account
// belongs to it, which reproduces the pre-handoff behaviour for every ordinary
// deployment; a tenant member that exists only inside its own organization
// projects that membership instead, which is the only one it has. Callers that
// need a specific organization's policy query organization_memberships directly
// and never come through here.
const userSource = ` FROM users u
	LEFT JOIN LATERAL (
		SELECT memberships.*
		FROM organization_memberships AS memberships
		JOIN organizations AS orgs ON orgs.id = memberships.organization_id
		WHERE memberships.account_id = u.id
		ORDER BY orgs.is_default DESC, memberships.created_at ASC, memberships.id ASC
		LIMIT 1
	) m ON TRUE`

// scanUser scans a single row into a *models.User.
func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	err := row.Scan(
		&u.ID,
		&u.AccountIncarnationID,
		&u.Email,
		&u.Username,
		&u.PasswordHash,
		&u.LocalPasswordLoginEnabled,
		&u.Role,
		&u.Permissions,
		&u.Enabled,
		&u.LibraryIDs,
		&u.MaxPlaybackQuality,
		&u.AccessPolicyRevision,
		&u.MaxStreams,
		&u.MaxTranscodes,
		&u.TranscodeAllowed,
		&u.AudioTranscodeAllowed,
		&u.MaxProfiles,
		&u.DownloadAllowed,
		&u.DownloadTranscodeAllowed,
		&u.RequestsAllowed,
		&u.AccessGroupID,
		&u.CreatedAt,
		&u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning user: %w", err)
	}
	return &u, nil
}

// scanUsers scans multiple rows into a []*models.User slice.
func scanUsers(rows pgx.Rows) ([]*models.User, error) {
	var users []*models.User
	for rows.Next() {
		var u models.User
		err := rows.Scan(
			&u.ID,
			&u.AccountIncarnationID,
			&u.Email,
			&u.Username,
			&u.PasswordHash,
			&u.LocalPasswordLoginEnabled,
			&u.Role,
			&u.Permissions,
			&u.Enabled,
			&u.LibraryIDs,
			&u.MaxPlaybackQuality,
			&u.AccessPolicyRevision,
			&u.MaxStreams,
			&u.MaxTranscodes,
			&u.TranscodeAllowed,
			&u.AudioTranscodeAllowed,
			&u.MaxProfiles,
			&u.DownloadAllowed,
			&u.DownloadTranscodeAllowed,
			&u.RequestsAllowed,
			&u.AccessGroupID,
			&u.CreatedAt,
			&u.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning user row: %w", err)
		}
		users = append(users, &u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating user rows: %w", err)
	}
	return users, nil
}

// Create inserts a new user with a bcrypt-hashed password and returns the created user.
func (r *UserRepository) Create(ctx context.Context, input models.CreateUserInput) (*models.User, error) {
	return r.createWithQuerier(ctx, r.pool, input)
}

type userCreateQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

type userMutationQuerier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CreateInTransaction applies the canonical user creation path on an existing
// transaction. Tenant member provisioning uses this so the account and its
// quota-bearing membership commit, or roll back, as one unit.
func (r *UserRepository) CreateInTransaction(ctx context.Context, tx pgx.Tx, input models.CreateUserInput) (*models.User, error) {
	return r.createWithQuerier(ctx, tx, input)
}

func (r *UserRepository) createWithQuerier(ctx context.Context, querier userCreateQuerier, input models.CreateUserInput) (*models.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	// Base columns that are always included.
	localPasswordLoginEnabled := true
	if input.LocalPasswordLoginEnabled != nil {
		localPasswordLoginEnabled = *input.LocalPasswordLoginEnabled
	}

	permissions := append([]string(nil), input.Permissions...)
	if input.Permissions == nil && input.Role != "admin" {
		permissions = DefaultUserPermissions()
	}
	permissions, err = NormalizePermissions(permissions)
	if err != nil {
		return nil, err
	}

	// Identity lives on users; the policy columns moved to
	// organization_memberships, so they are inserted there afterwards. A nil
	// pointer still stores NULL, which means "inherit from the access group".
	cols := []string{
		"email", "username", "password_hash", "local_password_login_enabled", "role",
	}
	args := []any{
		NormalizeEmail(input.Email),
		NormalizeUsername(input.Username),
		string(hash),
		localPasswordLoginEnabled,
		input.Role,
	}
	policyCols := []string{
		"permissions", "library_ids", "max_playback_quality", "max_streams", "max_transcodes",
		"transcode_allowed", "audio_transcode_allowed", "download_allowed",
		"download_transcode_allowed", "requests_allowed",
	}
	policyArgs := []any{
		permissions,
		input.LibraryIDs,
		normalizeQualityOverride(input.MaxPlaybackQuality),
		input.MaxStreams,
		input.MaxTranscodes,
		input.TranscodeAllowed,
		input.AudioTranscodeAllowed,
		input.DownloadAllowed,
		input.DownloadTranscodeAllowed,
		input.RequestsAllowed,
	}

	// Optional columns: nil means use DB default.
	if input.MaxProfiles != nil {
		policyCols = append(policyCols, "max_profiles")
		policyArgs = append(policyArgs, *input.MaxProfiles)
	}
	accessGroupID := input.AccessGroupID
	if input.Role == models.RoleAdmin {
		accessGroupID = nil
	}
	if accessGroupID != nil {
		policyCols = append(policyCols, "access_group_id")
		policyArgs = append(policyArgs, *accessGroupID)
	}

	// Build placeholders: $1, $2, ..., $N
	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	// Admins stay ungrouped: scope/action decisions are role-blind, so the
	// default group's ceilings would cap the server owner (mirrors the
	// exclusion in the assign_default_group_to_existing_users migration).
	defaultGroupExpr := ""
	if accessGroupID == nil && input.Role != models.RoleAdmin {
		defaultGroupExpr = `(SELECT g.id
			FROM access_groups g
			JOIN organizations o ON o.id = g.organization_id
			WHERE o.is_default
			  AND g.is_default)`
	}

	// The account row carries identity only now, so it is created first and the
	// policy lands on its default-organization membership.
	query := fmt.Sprintf("INSERT INTO users (%s) VALUES (%s) RETURNING id",
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
	)
	var accountID int
	if err := querier.QueryRow(ctx, query, args...).Scan(&accountID); err != nil {
		if isDuplicateKeyError(err) {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("creating user: %w", err)
	}
	if err := insertDefaultMembershipPolicy(ctx, querier, accountID, membershipLegacyRole(input.Role), accessGroupID, policyCols, policyArgs, defaultGroupExpr); err != nil {
		return nil, err
	}

	row := querier.QueryRow(ctx, `SELECT `+allColumns+userSource+` WHERE u.id = $1`, accountID)

	user, err := scanUser(row)
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, fmt.Errorf("%w: %s", ErrDuplicate, extractConstraint(err))
		}
		return nil, fmt.Errorf("creating user: %w", err)
	}

	return user, nil
}

// GetByID retrieves a user by their numeric ID.
func (r *UserRepository) GetByID(ctx context.Context, id int) (*models.User, error) {
	query := `SELECT ` + allColumns + userSource + ` WHERE u.id = $1`
	return scanUser(r.pool.QueryRow(ctx, query, id))
}

// GetByIDInTransaction reads an account through a caller-owned transaction.
func (r *UserRepository) GetByIDInTransaction(ctx context.Context, tx pgx.Tx, id int) (*models.User, error) {
	query := `SELECT ` + allColumns + userSource + ` WHERE u.id = $1`
	return scanUser(tx.QueryRow(ctx, query, id))
}

// GetByUsername retrieves a user by their username (case-insensitive).
func (r *UserRepository) GetByUsername(ctx context.Context, username string) (*models.User, error) {
	query := `SELECT ` + allColumns + userSource + ` WHERE u.username = $1`
	return scanUser(r.pool.QueryRow(ctx, query, NormalizeUsername(username)))
}

// GetByEmail retrieves a user by their email address (case-insensitive).
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*models.User, error) {
	query := `SELECT ` + allColumns + userSource + ` WHERE u.email = $1`
	return scanUser(r.pool.QueryRow(ctx, query, NormalizeEmail(email)))
}

// userUpdateColumn is one candidate column of a user update: it is written
// only when set, and bumpsAccessPolicy marks the columns whose change has to
// invalidate durable session/profile tokens by bumping
// access_policy_revision. Values are pre-computed, so every entry is safe to
// build even when set is false.
// membershipPolicyColumns are the columns 20260829085838_membership_policy_isolation
// moved off users, so an update naming one has to target the account's
// default-organization membership instead.
var membershipPolicyColumns = map[string]bool{
	"permissions": true, "library_ids": true, "max_playback_quality": true,
	"max_streams": true, "max_transcodes": true, "transcode_allowed": true,
	"audio_transcode_allowed": true, "max_profiles": true, "download_allowed": true,
	"download_transcode_allowed": true, "requests_allowed": true, "access_group_id": true,
}

type userUpdateColumn struct {
	column            string
	set               bool
	value             any
	bumpsAccessPolicy bool
	// mirrorsToMembership names the organization_memberships column that has to
	// track this users column. role/legacy_role are the same fact in two places,
	// and accessGroupSetClause reads legacy_role to decide an admin's group.
	mirrorsToMembership string
}

// accessGroupSetClause builds the SET clause and access-policy predicate for
// access_group_id given the next free placeholder index. access_group_id is
// handled outside the generic userUpdateColumn machinery because, unlike
// every other column, what gets written depends on the row's current role:
//
//   - Granting admin (input.Role == "admin") clears the group unconditionally.
//   - Changing role to anything else without naming a group lands the row on
//     the default group, but only if it was an admin (accounts are never
//     un-grouped by an unrelated role change).
//   - Setting a group on its own (input.Role == nil) is guarded by a CASE so
//     a write that races an admin promotion cannot leave the admin grouped.
//   - Otherwise (explicit NULL, or a group set alongside a non-admin role
//     change) the value is bound directly.
//
// Admin accounts are never grouped (see Create). Returns an empty setClause
// if access_group_id is not touched by this update.
//
// The default-group branch reads from a CTE (aliased in defaultGroupCTE)
// instead of inlining the subselect, because the same expression is spliced
// into both the SET clause and the access_policy_revision predicate — as a
// literal subselect it would run twice per UPDATE, but a CTE referenced more
// than once is materialized once by Postgres.
func accessGroupSetClause(input models.UpdateUserInput, argIndex int) (setClause, predicate, defaultGroupCTE string, args []any, nextArgIndex int) {
	// The clause now runs against organization_memberships, whose role column is
	// legacy_role; role itself stayed on users. The two are kept in step by
	// createWithQuerier and by the role branch of this same update.
	const isAdmin = "legacy_role = '" + models.RoleAdmin + "'"
	nextArgIndex = argIndex
	switch {
	case input.Role != nil && *input.Role == models.RoleAdmin:
		placeholder := fmt.Sprintf("$%d", argIndex)
		setClause = "access_group_id = " + placeholder
		args = []any{(*int64)(nil)}
		nextArgIndex++
	case input.Role != nil && !input.AccessGroupID.Set:
		defaultGroupCTE = "default_group AS (SELECT g.id FROM access_groups g JOIN organizations o ON o.id = g.organization_id WHERE o.is_default AND g.is_default)"
		expr := "(CASE WHEN " + isAdmin + " THEN (SELECT id FROM default_group) ELSE access_group_id END)"
		setClause = "access_group_id = " + expr
	case input.Role == nil && input.AccessGroupID.Set && input.AccessGroupID.Value != nil:
		placeholder := fmt.Sprintf("$%d", argIndex)
		// The cast pins the parameter type; inside a CASE the driver would
		// otherwise send it as text.
		expr := "(CASE WHEN " + isAdmin + " THEN NULL ELSE " + placeholder + "::bigint END)"
		setClause = "access_group_id = " + expr
		args = []any{input.AccessGroupID.Value}
		nextArgIndex++
	default:
		if !input.AccessGroupID.Set {
			return "", "", "", nil, argIndex
		}
		placeholder := fmt.Sprintf("$%d", argIndex)
		setClause = "access_group_id = " + placeholder
		args = []any{input.AccessGroupID.Value}
		nextArgIndex++
	}
	predicate = "access_group_id IS DISTINCT FROM " + strings.TrimPrefix(setClause, "access_group_id = ")
	return setClause, predicate, defaultGroupCTE, args, nextArgIndex
}

// Update modifies a user's fields. Only non-nil fields in the input are updated.
// If the input contains a Password, it is bcrypt-hashed before storage.
func (r *UserRepository) Update(ctx context.Context, id int, input models.UpdateUserInput) error {
	return r.updateWithQuerier(ctx, r.pool, id, input)
}

// UpdateInTransaction applies the canonical normalization, hashing and access
// revision behavior while participating in the caller's transaction.
func (r *UserRepository) UpdateInTransaction(ctx context.Context, tx pgx.Tx, id int, input models.UpdateUserInput) error {
	return r.updateWithQuerier(ctx, tx, id, input)
}

func (r *UserRepository) updateWithQuerier(ctx context.Context, querier userMutationQuerier, id int, input models.UpdateUserInput) error {
	var email *string
	if input.Email != nil {
		normalized := NormalizeEmail(*input.Email)
		email = &normalized
	}
	var username *string
	if input.Username != nil {
		normalized := NormalizeUsername(*input.Username)
		username = &normalized
	}
	var passwordHash *string
	if input.Password != nil {
		hash, err := bcrypt.GenerateFromPassword([]byte(*input.Password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hashing password: %w", err)
		}
		hashed := string(hash)
		passwordHash = &hashed
	}
	var permissions []string
	if input.Permissions != nil {
		normalized, err := NormalizePermissions(*input.Permissions)
		if err != nil {
			return err
		}
		permissions = normalized
	}

	// Library scope is resolved from users.library_ids on each request, so
	// changing it must not invalidate durable profile/session tokens — hence
	// no access-policy bump on that column.
	columns := []userUpdateColumn{
		{column: "email", set: email != nil, value: email},
		{column: "username", set: username != nil, value: username},
		{column: "password_hash", set: passwordHash != nil, value: passwordHash},
		{column: "local_password_login_enabled", set: input.LocalPasswordLoginEnabled != nil, value: input.LocalPasswordLoginEnabled},
		{column: "role", set: input.Role != nil, value: input.Role, bumpsAccessPolicy: true, mirrorsToMembership: "legacy_role"},
		{column: "permissions", set: input.Permissions != nil, value: permissions, bumpsAccessPolicy: true},
		{column: "enabled", set: input.Enabled != nil, value: input.Enabled, bumpsAccessPolicy: true},
		{column: "library_ids", set: input.LibraryIDs.Set, value: derefSlice(input.LibraryIDs.Value)},
		{
			column:            "max_playback_quality",
			set:               input.MaxPlaybackQuality.Set,
			value:             normalizeQualityOverride(input.MaxPlaybackQuality.Value),
			bumpsAccessPolicy: true,
		},
		{column: "max_streams", set: input.MaxStreams.Set, value: input.MaxStreams.Value},
		{column: "max_transcodes", set: input.MaxTranscodes.Set, value: input.MaxTranscodes.Value},
		{column: "transcode_allowed", set: input.TranscodeAllowed.Set, value: input.TranscodeAllowed.Value},
		{column: "audio_transcode_allowed", set: input.AudioTranscodeAllowed.Set, value: input.AudioTranscodeAllowed.Value},
		{column: "max_profiles", set: input.MaxProfiles != nil, value: input.MaxProfiles},
		{column: "download_allowed", set: input.DownloadAllowed.Set, value: input.DownloadAllowed.Value},
		{column: "download_transcode_allowed", set: input.DownloadTranscodeAllowed.Set, value: input.DownloadTranscodeAllowed.Value},
		{column: "requests_allowed", set: input.RequestsAllowed.Set, value: input.RequestsAllowed.Value},
	}

	setClauses := []string{}
	accessPolicyPredicates := []string{}
	args := []any{}
	argIndex := 1
	// Policy columns live on the account's membership now, so they are collected
	// separately and applied in their own statement below.
	membershipSet := []string{}
	membershipPredicates := []string{}
	membershipArgs := []any{}
	bumpFromAccountColumns := false
	for _, col := range columns {
		if !col.set {
			continue
		}
		if membershipPolicyColumns[col.column] {
			placeholder := fmt.Sprintf("$%d", len(membershipArgs)+1)
			membershipSet = append(membershipSet, fmt.Sprintf("%s = %s", col.column, placeholder))
			if col.bumpsAccessPolicy {
				membershipPredicates = append(
					membershipPredicates,
					fmt.Sprintf("%s IS DISTINCT FROM %s", col.column, placeholder),
				)
			}
			membershipArgs = append(membershipArgs, col.value)
			continue
		}
		placeholder := fmt.Sprintf("$%d", argIndex)
		setClauses = append(setClauses, fmt.Sprintf("%s = %s", col.column, placeholder))
		if col.mirrorsToMembership != "" {
			membershipSet = append(membershipSet, fmt.Sprintf("%s = $%d", col.mirrorsToMembership, len(membershipArgs)+1))
			mirrored := col.value
			if role, ok := col.value.(*string); ok && role != nil {
				mirrored = membershipLegacyRole(*role)
			}
			membershipArgs = append(membershipArgs, mirrored)
		}
		if col.bumpsAccessPolicy {
			// role and enabled still live on users, but the revision they bump is
			// the membership's. Comparing across tables afterwards would see the
			// value already written, so record the intent instead.
			bumpFromAccountColumns = true
			accessPolicyPredicates = append(
				accessPolicyPredicates,
				fmt.Sprintf("%s IS DISTINCT FROM %s", col.column, placeholder),
			)
		}
		args = append(args, col.value)
		argIndex++
	}

	// access_group_id is not a plain userUpdateColumn: what gets written
	// depends on the row's current role, so it is assembled directly rather
	// than through the generic column loop above.
	// access_group_id moved to the membership with the rest of the policy, so
	// its clause is built against the membership placeholder sequence.
	var defaultGroupCTE string
	if setClause, predicate, cte, groupArgs, _ := accessGroupSetClause(input, len(membershipArgs)+1); setClause != "" {
		membershipSet = append(membershipSet, setClause)
		membershipPredicates = append(membershipPredicates, predicate)
		defaultGroupCTE = cte
		membershipArgs = append(membershipArgs, groupArgs...)
	}

	if len(setClauses) == 0 {
		// Nothing left on the account row itself. Verify it exists, then apply
		// the policy half: an update that only touches policy columns now
		// changes nothing on users, and returning here would silently drop it.
		if _, err := scanUser(querier.QueryRow(ctx, `SELECT `+allColumns+userSource+` WHERE u.id = $1`, id)); err != nil {
			return err
		}
		return applyMembershipPolicyUpdate(ctx, querier, id, membershipSet, membershipPredicates, membershipArgs, bumpFromAccountColumns, defaultGroupCTE)
	}

	// Always bump updated_at.
	setClauses = append(setClauses, "updated_at = NOW()")

	query := fmt.Sprintf("UPDATE users SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "), argIndex)
	args = append(args, id)

	tag, err := querier.Exec(ctx, query, args...)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("%w: %s", ErrDuplicate, extractConstraint(err))
		}
		return fmt.Errorf("updating user: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return applyMembershipPolicyUpdate(ctx, querier, id, membershipSet, membershipPredicates, membershipArgs, bumpFromAccountColumns, defaultGroupCTE)
}

// applyMembershipPolicyUpdate writes the policy half of an account update to the
// account's default-organization membership, which is where those columns moved.
func applyMembershipPolicyUpdate(ctx context.Context, querier userMutationQuerier, id int, set, predicates []string, args []any, alwaysBump bool, defaultGroupCTE string) error {
	if len(set) == 0 && !alwaysBump {
		return nil
	}
	switch {
	case alwaysBump:
		set = append(set, "access_policy_revision = access_policy_revision + 1")
	case len(predicates) > 0:
		set = append(set, fmt.Sprintf(
			"access_policy_revision = CASE WHEN %s THEN access_policy_revision + 1 ELSE access_policy_revision END",
			strings.Join(predicates, " OR "),
		))
	}
	set = append(set, "updated_at = NOW()")
	args = append(args, id)
	prefix := ""
	if defaultGroupCTE != "" {
		prefix = "WITH " + defaultGroupCTE + " "
	}
	// The v1 writer marker is transaction-local, and this querier is often a
	// pool rather than a transaction, so a separate SET LOCAL would not survive
	// to this statement. Evaluating set_config in the WHERE marks the same
	// implicit transaction that performs the update.
	statement := fmt.Sprintf(
		`%sUPDATE organization_memberships SET %s
		 WHERE id = (
			SELECT memberships.id
			FROM organization_memberships AS memberships
			JOIN organizations AS orgs ON orgs.id = memberships.organization_id
			WHERE memberships.account_id = $%d
			ORDER BY orgs.is_default DESC, memberships.created_at ASC, memberships.id ASC
			LIMIT 1
		   )
		   AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`,
		prefix, strings.Join(set, ", "), len(args),
	)
	if _, err := querier.Exec(ctx, statement, args...); err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("%w: %s", ErrDuplicate, extractConstraint(err))
		}
		return fmt.Errorf("updating account membership policy: %w", err)
	}
	return nil
}

// Delete removes a user by their ID.
func (r *UserRepository) Delete(ctx context.Context, id int) error {
	return r.deleteWithQuerier(ctx, r.pool, id)
}

// DeleteInTransaction removes a user as part of a larger lifecycle change.
func (r *UserRepository) DeleteInTransaction(ctx context.Context, tx pgx.Tx, id int) error {
	return r.deleteWithQuerier(ctx, tx, id)
}

func (r *UserRepository) deleteWithQuerier(ctx context.Context, querier userMutationQuerier, id int) error {
	tag, err := querier.Exec(ctx, "DELETE FROM users WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

// List returns all users ordered by ID ascending.
func (r *UserRepository) List(ctx context.Context) ([]*models.User, error) {
	query := `SELECT ` + allColumns + userSource + ` ORDER BY u.id ASC`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()

	return scanUsers(rows)
}

// Count returns the number of users in the database.
func (r *UserRepository) Count(ctx context.Context) (int, error) {
	var count int
	if err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return count, nil
}

// CountInTransaction reads account cardinality on a caller-owned transaction.
func (r *UserRepository) CountInTransaction(ctx context.Context, tx pgx.Tx) (int, error) {
	var count int
	if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return count, nil
}

// isDuplicateKeyError checks if the error is a PostgreSQL unique_violation (code 23505).
func isDuplicateKeyError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// extractConstraint extracts the constraint name from a PgError for diagnostic messages.
func extractConstraint(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return "unknown"
}

// normalizeQualityOverride keeps the stored quality preset canonical while
// preserving nil (inherit).
func normalizeQualityOverride(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := access.NormalizePlaybackQuality(*value)
	return &normalized
}

// derefSlice maps a nil pointer to a NULL array and a non-nil pointer to its
// (possibly empty) slice, so Postgres distinguishes "inherit" from "none".
func derefSlice(value *[]int) []int {
	if value == nil {
		return nil
	}
	if *value == nil {
		return []int{}
	}
	return *value
}

// insertDefaultMembershipPolicy places a new account's policy on its membership
// in the default organization, which is where the authority moved.
// seed_legacy_membership_policy requires the v1 writer marker once the authority
// is finalized, and the marker is transaction-local, so it is set on the same
// querier immediately before the insert.
func insertDefaultMembershipPolicy(ctx context.Context, querier userCreateQuerier, accountID int, legacyRole string, explicitGroupID *int64, cols []string, args []any, defaultGroupExpr string) error {
	// The membership belongs to the organization that owns the account's group,
	// not necessarily the default one: tenancy creates member accounts against a
	// tenant organization and hands us that organization's group, and
	// organization_memberships_organization_access_group_fkey ties the pair
	// together. Fall back to the default organization only when no group was
	// supplied.
	organizationExpr := `(SELECT COALESCE(
		(SELECT g.organization_id FROM access_groups g WHERE g.id = $3),
		(SELECT id FROM organizations WHERE is_default)
	) WHERE set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL)`
	columns := append([]string{"organization_id", "account_id", "status", "legacy_role"}, cols...)
	values := []string{organizationExpr, "$1", "'active'", "$2"}
	insertArgs := []any{accountID, legacyRole, explicitGroupID}
	for i, value := range args {
		values = append(values, fmt.Sprintf("$%d", i+4))
		insertArgs = append(insertArgs, value)
	}
	if defaultGroupExpr != "" {
		columns = append(columns, "access_group_id")
		values = append(values, defaultGroupExpr)
	}
	statement := fmt.Sprintf(
		"INSERT INTO organization_memberships (%s) VALUES (%s) ON CONFLICT (organization_id, account_id) DO NOTHING",
		strings.Join(columns, ", "), strings.Join(values, ", "),
	)
	if _, err := querier.Exec(ctx, statement, insertArgs...); err != nil {
		return fmt.Errorf("creating account membership policy: %w", err)
	}
	return nil
}

// membershipLegacyRole narrows an account role to the two values
// organization_memberships.legacy_role accepts. Roles beyond admin are ordinary
// members as far as tenant membership is concerned; the account keeps its full
// role on users.
func membershipLegacyRole(role string) string {
	if role == models.RoleAdmin {
		return models.RoleAdmin
	}
	return "user"
}
