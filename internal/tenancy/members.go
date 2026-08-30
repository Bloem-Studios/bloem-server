package tenancy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/sessioninvalidation"
)

var (
	ErrMemberNotFound                   = errors.New("tenancy: member not found")
	ErrSlotQuotaExceeded                = errors.New("tenancy: member slot quota exceeded")
	ErrTenantFrozen                     = errors.New("tenancy: tenant is frozen")
	ErrIdempotencyConflict              = errors.New("tenancy: idempotency command conflicts with prior request")
	ErrUsernameConflict                 = errors.New("tenancy: member username conflicts")
	ErrInvalidMemberCommand             = errors.New("tenancy: invalid member command")
	ErrMembershipPolicyWriteUnavailable = errors.New("tenancy: membership policy rollout is not writable")
)

// CreateMemberInput is the credential-bearing command for a tenant member.
// Password must remain request-local and is never returned by MemberService.
type CreateMemberInput struct {
	Username string
	Email    string
	Password string
}

type memberUserRepository interface {
	GetByID(context.Context, int) (*models.User, error)
}

type memberSessionRepository interface {
	RevokeAllByUser(context.Context, int) error
	RevokeAllByUserInTransaction(context.Context, pgx.Tx, int) error
}

type memberAccountProvisioner interface {
	CreateUserInTransaction(context.Context, pgx.Tx, models.CreateUserInput) (*models.User, bool, error)
	UpdateUser(context.Context, int, models.UpdateUserInput) (bool, error)
	UpdateUserInTransaction(context.Context, pgx.Tx, int, models.UpdateUserInput) (bool, error)
	DeleteUser(context.Context, int) error
	DeleteUserInTransaction(context.Context, pgx.Tx, int) error
}

type memberResourcePurger interface {
	PurgeOrganizationResources(context.Context, uuid.UUID, int) error
}

// UpdateMemberInput contains the display-safe identity metadata a reseller may
// change. Password replacement has a separate method so it cannot be echoed by
// an ordinary update response or error.
type UpdateMemberInput struct {
	Username *string
	Email    *string
}

// MemberService owns the tenant-scoped account lifecycle.
type MemberService struct {
	pool           *pgxpool.Pool
	store          *Store
	accounts       memberAccountProvisioner
	users          memberUserRepository
	sessions       memberSessionRepository
	resources      memberResourcePurger
	compatSessions func(context.Context, int) error
}

// SetResourcePurger installs the Task 4 profile/device lifecycle adapter used
// when a tenant membership is removed while the global account survives.
func (s *MemberService) SetResourcePurger(purger memberResourcePurger) {
	if s != nil {
		s.resources = purger
	}
}

// SetCompatSessionInvalidator installs the volatile compatibility-session
// eviction hook. Security mutations call it only after their native database
// transaction has committed.
func (s *MemberService) SetCompatSessionInvalidator(invalidator func(context.Context, int) error) {
	if s != nil {
		s.compatSessions = invalidator
	}
}

func (s *MemberService) invalidateCompatSessionsAfterCommit(ctx context.Context, userID int, operation string) error {
	if s == nil || s.compatSessions == nil {
		return nil
	}
	if err := sessioninvalidation.Run(ctx, func(callbackCtx context.Context) error {
		return s.compatSessions(callbackCtx, userID)
	}); err != nil {
		// The callback may wrap infrastructure details. Keep them in the returned
		// error chain for operators while avoiding credential-bearing log output.
		slog.ErrorContext(ctx, "compat session invalidation failed after committed member mutation",
			"operation", operation, "user_id", userID)
		return fmt.Errorf("tenancy: invalidate compat sessions after committed %s: %w", operation, err)
	}
	return nil
}

// NewMemberService builds a tenant-member service from the native account and
// session services. The account provisioner remains the only user-creation
// path; MemberService supplies its tenant-locked transaction.
func NewMemberService(
	pool *pgxpool.Pool,
	accounts memberAccountProvisioner,
	users memberUserRepository,
	sessions memberSessionRepository,
) *MemberService {
	return &MemberService{pool: pool, store: NewStore(pool), accounts: accounts, users: users, sessions: sessions}
}

var memberCommandNamespace = uuid.MustParse("3fd6b462-88cf-4e75-80b4-aec84b8a1e0e")

func memberCommandID(tenantID uuid.UUID, key string) uuid.UUID {
	return uuid.NewSHA1(memberCommandNamespace, []byte(tenantID.String()+"\x00"+key))
}

func validMemberIdentity(username, email, password string) bool {
	if username == "" || len(username) > 255 || email == "" || len(email) > 320 || len(password) < 8 || len(password) > 72 {
		return false
	}
	for _, value := range []string{username, email} {
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return false
		}
	}
	parsed, err := mail.ParseAddress(email)
	return err == nil && parsed.Address == email && strings.Contains(email, "@")
}

func validIdempotencyKey(key string) bool {
	return key != "" && len(key) <= 255 && strings.IndexFunc(key, unicode.IsControl) < 0
}

func memberCommandDigest(input CreateMemberInput) ([]byte, error) {
	canonical, err := json.Marshal(struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}{input.Username, input.Email, input.Password})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	return digest[:], nil
}

// Create atomically claims the idempotency command, enforces the live member
// quota, creates the native account, and adds its tenant membership.
func (s *MemberService) Create(
	ctx context.Context,
	tenantID uuid.UUID,
	idempotencyKey string,
	input CreateMemberInput,
) (user models.User, replay bool, err error) {
	key := strings.TrimSpace(idempotencyKey)
	input.Username = strings.TrimSpace(input.Username)
	input.Email = strings.TrimSpace(input.Email)
	if s == nil || s.pool == nil || s.accounts == nil || s.users == nil || tenantID == uuid.Nil ||
		!validIdempotencyKey(key) || !validMemberIdentity(input.Username, input.Email, input.Password) {
		return models.User{}, false, ErrInvalidMemberCommand
	}
	digest, err := memberCommandDigest(input)
	if err != nil {
		return models.User{}, false, ErrInvalidMemberCommand
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.User{}, false, fmt.Errorf("tenancy: begin member create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tenant, err := s.store.lockTenantOrganization(ctx, tx, tenantID)
	if errors.Is(err, ErrTenantOrganizationNotFound) {
		return models.User{}, false, ErrMemberNotFound
	}
	if err != nil {
		return models.User{}, false, err
	}

	var (
		requestHash                     string
		existingAccountID               int
		existingUsername, existingEmail string
		deletedAt                       *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT request_hash, result_account_id, result_username, result_email, deleted_at
		FROM tenant_member_command_receipts
		WHERE organization_id = $1 AND idempotency_key = $2`, tenantID, key).
		Scan(&requestHash, &existingAccountID, &existingUsername, &existingEmail, &deletedAt)
	switch {
	case err == nil:
		if bcrypt.CompareHashAndPassword([]byte(requestHash), digest) != nil {
			return models.User{}, false, ErrIdempotencyConflict
		}
		return models.User{ID: existingAccountID, Username: existingUsername, Email: existingEmail, Enabled: true}, true, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return models.User{}, false, fmt.Errorf("tenancy: read member command: %w", err)
	}

	// Rolling compatibility for commands claimed by the pre-receipt binary.
	// The deterministic membership id lets the first replay adopt the old
	// claim; from then on the immutable receipt is authoritative.
	commandID := memberCommandID(tenantID, key)
	err = tx.QueryRow(ctx, `SELECT account_id FROM organization_memberships WHERE id=$1 AND organization_id=$2`, commandID, tenantID).Scan(&existingAccountID)
	if err == nil {
		existing, loadErr := s.users.GetByID(ctx, existingAccountID)
		if loadErr != nil {
			return models.User{}, false, fmt.Errorf("tenancy: load legacy replayed member: %w", loadErr)
		}
		if existing.Username != input.Username || existing.Email != input.Email || bcrypt.CompareHashAndPassword([]byte(existing.PasswordHash), []byte(input.Password)) != nil {
			return models.User{}, false, ErrIdempotencyConflict
		}
		requestHashBytes, hashErr := bcrypt.GenerateFromPassword(digest, bcrypt.DefaultCost)
		if hashErr != nil {
			return models.User{}, false, fmt.Errorf("tenancy: hash legacy member command: %w", hashErr)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO tenant_member_command_receipts
			(organization_id,idempotency_key,request_hash,result_account_id,result_username,result_email)
			VALUES ($1,$2,$3,$4,$5,$6)`, tenantID, key, string(requestHashBytes), existing.ID, existing.Username, existing.Email); err != nil {
			return models.User{}, false, fmt.Errorf("tenancy: adopt legacy member command: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return models.User{}, false, fmt.Errorf("tenancy: commit legacy member replay: %w", err)
		}
		return *existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.User{}, false, fmt.Errorf("tenancy: read legacy member command: %w", err)
	}

	if tenant.Frozen {
		return models.User{}, false, ErrTenantFrozen
	}
	used, err := tenantMembershipCount(ctx, tx, tenantID)
	if err != nil {
		return models.User{}, false, err
	}
	if used >= tenant.Slots {
		return models.User{}, false, ErrSlotQuotaExceeded
	}
	if err := ensureTenantDefaultAccessGroup(ctx, tx, tenantID); err != nil {
		return models.User{}, false, err
	}
	var accessGroupID int64
	if err := tx.QueryRow(ctx, `
		SELECT id FROM access_groups
		WHERE organization_id = $1 AND is_default`, tenantID).Scan(&accessGroupID); err != nil {
		return models.User{}, false, fmt.Errorf("tenancy: load member tenant default access group: %w", err)
	}

	created, conflict, err := s.accounts.CreateUserInTransaction(ctx, tx, models.CreateUserInput{
		Username:      input.Username,
		Email:         input.Email,
		Password:      input.Password,
		Role:          legacyRoleUser,
		AccessGroupID: &accessGroupID,
	})
	if conflict {
		return models.User{}, false, ErrUsernameConflict
	}
	if err != nil {
		return models.User{}, false, fmt.Errorf("tenancy: create member account: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO organization_memberships (id, organization_id, account_id, status, legacy_role)
		SELECT $1, $2, $3, $4, $5
		WHERE set_config('bloem.membership_policy_writer',
				CASE WHEN (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized'
				     THEN 'v1' ELSE '' END, true) IS NOT NULL
		ON CONFLICT (organization_id, account_id) DO UPDATE
		SET status = EXCLUDED.status, legacy_role = EXCLUDED.legacy_role`,
		uuid.New(), tenantID, created.ID, MembershipActive, legacyRoleUser); err != nil {
		return models.User{}, false, fmt.Errorf("tenancy: create member membership: %w", err)
	}
	requestHashBytes, err := bcrypt.GenerateFromPassword(digest, bcrypt.DefaultCost)
	if err != nil {
		return models.User{}, false, fmt.Errorf("tenancy: hash member command: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO tenant_member_command_receipts
		(organization_id,idempotency_key,request_hash,result_account_id,result_username,result_email)
		VALUES ($1,$2,$3,$4,$5,$6)`, tenantID, key, string(requestHashBytes), created.ID, created.Username, created.Email); err != nil {
		return models.User{}, false, fmt.Errorf("tenancy: record member command: %w", err)
	}
	if tenant.ownerAccountID == nil {
		if _, err = tx.Exec(ctx, `
			UPDATE organizations SET owner_account_id = $2, status = $3, updated_at = now()
			WHERE id = $1`, tenantID, created.ID, OrganizationActive); err != nil {
			return models.User{}, false, fmt.Errorf("tenancy: activate member tenant: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return models.User{}, false, fmt.Errorf("tenancy: commit member create: %w", err)
	}
	s.store.invalidateTenantLimitsCache()
	return *created, false, nil
}

// CreateInTransaction creates a tenant member on a caller-owned transaction.
// Durable lifecycle coordinators use this path so the generated account and
// membership identities are committed atomically with their receipt.
func (s *MemberService) CreateInTransaction(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, input CreateMemberInput) (models.User, uuid.UUID, error) {
	input.Username = strings.TrimSpace(input.Username)
	input.Email = strings.TrimSpace(input.Email)
	if s == nil || s.store == nil || s.accounts == nil || tx == nil || tenantID == uuid.Nil ||
		!validMemberIdentity(input.Username, input.Email, input.Password) {
		return models.User{}, uuid.Nil, ErrInvalidMemberCommand
	}
	tenant, err := s.store.lockTenantOrganization(ctx, tx, tenantID)
	if errors.Is(err, ErrTenantOrganizationNotFound) {
		return models.User{}, uuid.Nil, ErrMemberNotFound
	}
	if err != nil {
		return models.User{}, uuid.Nil, err
	}
	if tenant.Frozen {
		return models.User{}, uuid.Nil, ErrTenantFrozen
	}
	used, err := tenantMembershipCount(ctx, tx, tenantID)
	if err != nil {
		return models.User{}, uuid.Nil, err
	}
	if used >= tenant.Slots {
		return models.User{}, uuid.Nil, ErrSlotQuotaExceeded
	}
	if err := ensureTenantDefaultAccessGroup(ctx, tx, tenantID); err != nil {
		return models.User{}, uuid.Nil, err
	}
	var accessGroupID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, tenantID).Scan(&accessGroupID); err != nil {
		return models.User{}, uuid.Nil, fmt.Errorf("tenancy: load member tenant default access group: %w", err)
	}
	created, conflict, err := s.accounts.CreateUserInTransaction(ctx, tx, models.CreateUserInput{
		Username: input.Username, Email: input.Email, Password: input.Password, Role: legacyRoleUser, AccessGroupID: &accessGroupID,
	})
	if conflict {
		return models.User{}, uuid.Nil, ErrUsernameConflict
	}
	if err != nil {
		return models.User{}, uuid.Nil, fmt.Errorf("tenancy: create member account: %w", err)
	}
	membershipID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO organization_memberships (id,organization_id,account_id,status,legacy_role) SELECT $1,$2,$3,$4,$5 WHERE set_config('bloem.membership_policy_writer',
				CASE WHEN (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized'
				     THEN 'v1' ELSE '' END, true) IS NOT NULL
		ON CONFLICT (organization_id, account_id) DO UPDATE
		SET status = EXCLUDED.status, legacy_role = EXCLUDED.legacy_role`, membershipID, tenantID, created.ID, MembershipActive, legacyRoleUser); err != nil {
		return models.User{}, uuid.Nil, fmt.Errorf("tenancy: create member membership: %w", err)
	}
	if tenant.ownerAccountID == nil {
		if _, err := tx.Exec(ctx, `UPDATE organizations SET owner_account_id=$2,status=$3,updated_at=now() WHERE id=$1`, tenantID, created.ID, OrganizationActive); err != nil {
			return models.User{}, uuid.Nil, fmt.Errorf("tenancy: activate member tenant: %w", err)
		}
	}
	return *created, membershipID, nil
}

// CompleteCreateAfterCommit invalidates quota reads after a coordinated create.
func (s *MemberService) CompleteCreateAfterCommit() {
	if s != nil && s.store != nil {
		s.store.invalidateTenantLimitsCache()
	}
}

// List returns only users with a membership in the asserted live tenant.
func (s *MemberService) List(ctx context.Context, tenantID uuid.UUID) ([]models.User, error) {
	if s == nil || s.pool == nil || tenantID == uuid.Nil {
		return nil, ErrMemberNotFound
	}
	if _, err := s.store.GetTenantOrganization(ctx, tenantID); err != nil {
		if errors.Is(err, ErrTenantOrganizationNotFound) {
			return nil, ErrMemberNotFound
		}
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT m.account_id
		FROM organization_memberships m
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.organization_id = $1 AND o.external_service_id IS NOT NULL
		ORDER BY m.account_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("tenancy: list member ids: %w", err)
	}
	defer rows.Close()
	out := []models.User{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("tenancy: scan member id: %w", err)
		}
		member, err := s.users.GetByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("tenancy: load member: %w", err)
		}
		out = append(out, *member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenancy: iterate members: %w", err)
	}
	return out, nil
}

// RequireMembership proves that userID belongs to the live tenant asserted by
// the request URL. Callers use the same check before nested resource access.
func (s *MemberService) RequireMembership(ctx context.Context, tenantID uuid.UUID, userID int) error {
	if s == nil || s.pool == nil || tenantID == uuid.Nil || userID <= 0 {
		return ErrMemberNotFound
	}
	var present bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM organization_memberships m
			JOIN organizations o ON o.id = m.organization_id
			WHERE m.organization_id = $1 AND m.account_id = $2
			  AND o.external_service_id IS NOT NULL
		)`, tenantID, userID).Scan(&present); err != nil {
		return fmt.Errorf("tenancy: resolve member: %w", err)
	}
	if !present {
		return ErrMemberNotFound
	}
	return nil
}

// Get returns a member only through the asserted tenant membership.
func (s *MemberService) Get(ctx context.Context, tenantID uuid.UUID, userID int) (models.User, error) {
	if err := s.RequireMembership(ctx, tenantID, userID); err != nil {
		return models.User{}, err
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil || user == nil {
		if err == nil {
			err = ErrMemberNotFound
		}
		return models.User{}, fmt.Errorf("tenancy: load scoped member: %w", err)
	}
	return *user, nil
}

// Update changes member identity through the native user repository.
func (s *MemberService) Update(ctx context.Context, tenantID uuid.UUID, userID int, input UpdateMemberInput) (models.User, error) {
	tx, err := s.beginMemberMutation(ctx, tenantID, userID)
	if err != nil {
		return models.User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.UpdateInTransaction(ctx, tx, tenantID, userID, input); err != nil {
		return models.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.User{}, fmt.Errorf("tenancy: commit member update: %w", err)
	}
	if err := s.invalidateCompatSessionsAfterCommit(ctx, userID, "identity update"); err != nil {
		return models.User{}, err
	}
	updated, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return models.User{}, fmt.Errorf("tenancy: load updated member: %w", err)
	}
	return *updated, nil
}

func (s *MemberService) beginMemberMutation(ctx context.Context, tenantID uuid.UUID, userID int) (pgx.Tx, error) {
	if s == nil || s.pool == nil || tenantID == uuid.Nil || userID <= 0 {
		return nil, ErrMemberNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("tenancy: begin member mutation: %w", err)
	}
	if _, err := s.store.lockTenantOrganization(ctx, tx, tenantID); err != nil {
		_ = tx.Rollback(ctx)
		if errors.Is(err, ErrTenantOrganizationNotFound) {
			return nil, ErrMemberNotFound
		}
		return nil, err
	}
	var present int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM organization_memberships WHERE organization_id=$1 AND account_id=$2 FOR UPDATE`, tenantID, userID).Scan(&present); err != nil {
		_ = tx.Rollback(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMemberNotFound
		}
		return nil, fmt.Errorf("tenancy: lock member: %w", err)
	}
	return tx, nil
}

// Suspend disables the account and its membership, retaining all data.
func (s *MemberService) Suspend(ctx context.Context, tenantID uuid.UUID, userID int) (models.User, error) {
	return s.setSuspended(ctx, tenantID, userID, true)
}

// Resume re-enables the account and its membership.
func (s *MemberService) Resume(ctx context.Context, tenantID uuid.UUID, userID int) (models.User, error) {
	return s.setSuspended(ctx, tenantID, userID, false)
}

func (s *MemberService) setSuspended(ctx context.Context, tenantID uuid.UUID, userID int, suspended bool) (models.User, error) {
	tx, err := s.beginMemberMutation(ctx, tenantID, userID)
	if err != nil {
		return models.User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.SetSuspendedInTransaction(ctx, tx, tenantID, userID, suspended); err != nil {
		return models.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.User{}, fmt.Errorf("tenancy: commit member state: %w", err)
	}
	if suspended {
		if err := s.invalidateCompatSessionsAfterCommit(ctx, userID, "suspension"); err != nil {
			return models.User{}, err
		}
	}
	updated, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return models.User{}, fmt.Errorf("tenancy: load member state: %w", err)
	}
	return *updated, nil
}

// ResetPassword replaces the native password and revokes all login sessions.
func (s *MemberService) ResetPassword(ctx context.Context, tenantID uuid.UUID, userID int, password string) (models.User, error) {
	tx, err := s.beginMemberMutation(ctx, tenantID, userID)
	if err != nil {
		return models.User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.ResetPasswordInTransaction(ctx, tx, tenantID, userID, password); err != nil {
		return models.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.User{}, fmt.Errorf("tenancy: commit member password reset: %w", err)
	}
	if err := s.invalidateCompatSessionsAfterCommit(ctx, userID, "password reset"); err != nil {
		return models.User{}, err
	}
	updated, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return models.User{}, fmt.Errorf("tenancy: load reset member: %w", err)
	}
	return *updated, nil
}

// Delete removes the tenant membership and only removes the global account
// when it has no other membership, ownership, or platform-admin obligation.
// Ownership, freeze recomputation, membership removal/account deletion, and
// the durable tombstone share one tenant-row-locked transaction.
func (s *MemberService) Delete(ctx context.Context, tenantID uuid.UUID, userID int) error {
	if s == nil || s.pool == nil || tenantID == uuid.Nil || userID <= 0 {
		return ErrMemberNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tenancy: begin member delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.DeleteInTransaction(ctx, tx, tenantID, userID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("tenancy: commit member delete: %w", err)
	}
	return s.CompleteDeleteAfterCommit(ctx, tenantID, userID)
}

// DeleteInTransaction applies tenant membership/account deletion on a
// caller-owned transaction. Lifecycle callers resolve and lock the exact
// membership before invoking it; legacy callers may call it directly and it
// acquires the same domain locks itself.
func (s *MemberService) DeleteInTransaction(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, userID int) error {
	if s == nil || tx == nil || tenantID == uuid.Nil || userID <= 0 {
		return ErrMemberNotFound
	}
	tenant, err := s.store.lockTenantOrganization(ctx, tx, tenantID)
	if errors.Is(err, ErrTenantOrganizationNotFound) {
		return ErrMemberNotFound
	}
	if err != nil {
		return err
	}
	var membershipStatus MembershipStatus
	err = tx.QueryRow(ctx, `SELECT status FROM organization_memberships WHERE organization_id=$1 AND account_id=$2 FOR UPDATE`, tenantID, userID).Scan(&membershipStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		var tombstoned bool
		if receiptErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenant_member_command_receipts WHERE organization_id=$1 AND result_account_id=$2 AND deleted_at IS NOT NULL)`, tenantID, userID).Scan(&tombstoned); receiptErr != nil {
			return fmt.Errorf("tenancy: check member delete receipt: %w", receiptErr)
		}
		if !tombstoned {
			return ErrMemberNotFound
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("tenancy: lock member delete: %w", err)
	}

	var (
		role            string
		username        string
		email           string
		membershipCount int
		otherOwnerCount int
		platformOwner   bool
	)
	if err := tx.QueryRow(ctx, `SELECT role,username,email FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&role, &username, &email); err != nil {
		return fmt.Errorf("tenancy: lock member account: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE account_id=$1`, userID).Scan(&membershipCount); err != nil {
		return fmt.Errorf("tenancy: count member obligations: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE owner_account_id=$1 AND id<>$2`, userID, tenantID).Scan(&otherOwnerCount); err != nil {
		return fmt.Errorf("tenancy: count member ownerships: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_security WHERE owner_account_id=$1)`, userID).Scan(&platformOwner); err != nil {
		return fmt.Errorf("tenancy: check platform ownership: %w", err)
	}

	deletingOwner := tenant.ownerAccountID != nil && *tenant.ownerAccountID == userID
	var replacement *int
	if deletingOwner {
		var candidate int
		err := tx.QueryRow(ctx, `
			SELECT m.account_id
			FROM organization_memberships m
			JOIN users u ON u.id=m.account_id
			WHERE m.organization_id=$1 AND m.account_id<>$2
			  AND m.status='active' AND u.enabled
			ORDER BY m.account_id LIMIT 1`, tenantID, userID).Scan(&candidate)
		if err == nil {
			replacement = &candidate
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("tenancy: choose eligible replacement owner: %w", err)
		}
	}
	ownerAfter := tenant.ownerAccountID
	if deletingOwner {
		ownerAfter = replacement
	}
	usedBefore, err := tenantMembershipCount(ctx, tx, tenantID)
	if err != nil {
		return err
	}
	usedAfter := usedBefore - 1
	status, reason := OrganizationActive, ""
	switch {
	case tenant.FrozenReason == TenantFrozenReasonAdmin:
		status, reason = OrganizationSuspended, TenantFrozenReasonAdmin
	case usedAfter > tenant.Slots:
		status, reason = OrganizationSuspended, TenantFrozenReasonQuota
	case ownerAfter == nil:
		status = OrganizationInitializing
	}
	if _, err := tx.Exec(ctx, `UPDATE organizations SET owner_account_id=$2,status=$3,suspension_reason=$4,updated_at=now() WHERE id=$1`, tenantID, ownerAfter, status, reason); err != nil {
		return fmt.Errorf("tenancy: update tenant after member delete: %w", err)
	}

	deleteGlobal := membershipCount == 1 && otherOwnerCount == 0 && !platformOwner && role != legacyRoleAdmin
	if deleteGlobal {
		if err := s.accounts.DeleteUserInTransaction(ctx, tx, userID); err != nil {
			return fmt.Errorf("tenancy: delete unowned member account: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `DELETE FROM user_device_settings WHERE user_id=$1 AND profile_id IN (SELECT id FROM user_profiles WHERE user_id=$1 AND organization_id=$2)`, userID, tenantID); err != nil {
			return fmt.Errorf("tenancy: delete scoped member device settings: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_devices WHERE user_id=$1 AND profile_id IN (SELECT id FROM user_profiles WHERE user_id=$1 AND organization_id=$2)`, userID, tenantID); err != nil {
			return fmt.Errorf("tenancy: delete scoped member devices: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, tenantID, userID); err != nil {
			return fmt.Errorf("tenancy: delete scoped member membership: %w", err)
		}
	}
	receiptTag, err := tx.Exec(ctx, `UPDATE tenant_member_command_receipts SET deleted_at=COALESCE(deleted_at,now()) WHERE organization_id=$1 AND result_account_id=$2`, tenantID, userID)
	if err != nil {
		return fmt.Errorf("tenancy: tombstone member command: %w", err)
	}
	if receiptTag.RowsAffected() == 0 {
		// Imported and pre-receipt members have no create command to mark. Keep a
		// reserved (control-prefixed, therefore API-invalid) deletion tombstone so
		// their DELETE retry is just as durable and idempotent.
		tombstoneDigest := sha256.Sum256([]byte("tenant-member-delete-tombstone-v1"))
		tombstoneHash, hashErr := bcrypt.GenerateFromPassword(tombstoneDigest[:], bcrypt.DefaultCost)
		if hashErr != nil {
			return fmt.Errorf("tenancy: hash member delete tombstone: %w", hashErr)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tenant_member_command_receipts
			(organization_id,idempotency_key,request_hash,result_account_id,result_username,result_email,deleted_at)
			VALUES ($1,$2,$3,$4,$5,$6,now())`, tenantID, fmt.Sprintf("\x1fdelete:%d", userID), string(tombstoneHash), userID, username, email); err != nil {
			return fmt.Errorf("tenancy: record member delete tombstone: %w", err)
		}
	}
	return nil
}

// CompleteDeleteAfterCommit performs cache and external resource effects only
// after the durable delete transaction has committed. A surviving account
// means the delete removed one membership and its organization-scoped
// resources still need purging.
func (s *MemberService) CompleteDeleteAfterCommit(ctx context.Context, tenantID uuid.UUID, userID int) error {
	s.store.invalidateTenantLimitsCache()
	if s.resources == nil {
		return nil
	}
	var accountPresent bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, userID).Scan(&accountPresent); err != nil {
		return fmt.Errorf("tenancy: check deleted member account: %w", err)
	}
	if accountPresent {
		if err := s.resources.PurgeOrganizationResources(ctx, tenantID, userID); err != nil {
			return fmt.Errorf("tenancy: purge scoped member resources: %w", err)
		}
	}
	return nil
}
