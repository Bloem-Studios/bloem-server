package tenancy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/Silo-Server/silo-server/internal/models"
)

var (
	ErrMemberNotFound       = errors.New("tenancy: member not found")
	ErrSlotQuotaExceeded    = errors.New("tenancy: member slot quota exceeded")
	ErrTenantFrozen         = errors.New("tenancy: tenant is frozen")
	ErrIdempotencyConflict  = errors.New("tenancy: idempotency command conflicts with prior request")
	ErrUsernameConflict     = errors.New("tenancy: member username conflicts")
	ErrInvalidMemberCommand = errors.New("tenancy: invalid member command")
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
}

type memberAccountProvisioner interface {
	CreateUserInTransaction(context.Context, pgx.Tx, models.CreateUserInput) (*models.User, bool, error)
	UpdateUser(context.Context, int, models.UpdateUserInput) (bool, error)
	DeleteUser(context.Context, int) error
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
	pool     *pgxpool.Pool
	store    *Store
	accounts memberAccountProvisioner
	users    memberUserRepository
	sessions memberSessionRepository
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
		key == "" || len(key) > 256 || input.Username == "" || input.Email == "" || input.Password == "" {
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

	commandID := memberCommandID(tenantID, key)
	var existingAccountID int
	err = tx.QueryRow(ctx, `
		SELECT account_id FROM organization_memberships
		WHERE id = $1 AND organization_id = $2`, commandID, tenantID).Scan(&existingAccountID)
	switch {
	case err == nil:
		existing, loadErr := s.users.GetByID(ctx, existingAccountID)
		if loadErr != nil {
			return models.User{}, false, fmt.Errorf("tenancy: load replayed member: %w", loadErr)
		}
		if existing.Username != input.Username || existing.Email != input.Email ||
			bcrypt.CompareHashAndPassword([]byte(existing.PasswordHash), []byte(input.Password)) != nil {
			return models.User{}, false, ErrIdempotencyConflict
		}
		return *existing, true, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return models.User{}, false, fmt.Errorf("tenancy: read member command: %w", err)
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
		VALUES ($1, $2, $3, $4, $5)`, commandID, tenantID, created.ID, MembershipActive, legacyRoleUser); err != nil {
		return models.User{}, false, fmt.Errorf("tenancy: create member membership: %w", err)
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
	if err := s.RequireMembership(ctx, tenantID, userID); err != nil {
		return models.User{}, err
	}
	update := models.UpdateUserInput{}
	if input.Username != nil {
		username := strings.TrimSpace(*input.Username)
		if username == "" {
			return models.User{}, ErrInvalidMemberCommand
		}
		update.Username = &username
	}
	if input.Email != nil {
		email := strings.TrimSpace(*input.Email)
		if email == "" {
			return models.User{}, ErrInvalidMemberCommand
		}
		update.Email = &email
	}
	if update.Username == nil && update.Email == nil {
		return models.User{}, ErrInvalidMemberCommand
	}
	conflict, err := s.accounts.UpdateUser(ctx, userID, update)
	if conflict {
		return models.User{}, ErrUsernameConflict
	}
	if err != nil {
		return models.User{}, fmt.Errorf("tenancy: update member: %w", err)
	}
	if update.Username != nil && s.sessions != nil {
		if err := s.sessions.RevokeAllByUser(ctx, userID); err != nil {
			return models.User{}, fmt.Errorf("tenancy: revoke renamed member sessions: %w", err)
		}
	}
	return s.Get(ctx, tenantID, userID)
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
	if err := s.RequireMembership(ctx, tenantID, userID); err != nil {
		return models.User{}, err
	}
	enabled := !suspended
	if _, err := s.accounts.UpdateUser(ctx, userID, models.UpdateUserInput{Enabled: &enabled}); err != nil {
		return models.User{}, fmt.Errorf("tenancy: update member enabled state: %w", err)
	}
	status := MembershipActive
	if suspended {
		status = MembershipSuspended
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE organization_memberships
		SET status = $3, security_revision = security_revision + 1, updated_at = now()
		WHERE organization_id = $1 AND account_id = $2`, tenantID, userID, status)
	if err != nil {
		return models.User{}, fmt.Errorf("tenancy: update member status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return models.User{}, ErrMemberNotFound
	}
	if suspended && s.sessions != nil {
		if err := s.sessions.RevokeAllByUser(ctx, userID); err != nil {
			return models.User{}, fmt.Errorf("tenancy: revoke suspended member sessions: %w", err)
		}
	}
	return s.Get(ctx, tenantID, userID)
}

// ResetPassword replaces the native password and revokes all login sessions.
func (s *MemberService) ResetPassword(ctx context.Context, tenantID uuid.UUID, userID int, password string) (models.User, error) {
	if err := s.RequireMembership(ctx, tenantID, userID); err != nil {
		return models.User{}, err
	}
	if password == "" {
		return models.User{}, ErrInvalidMemberCommand
	}
	if _, err := s.accounts.UpdateUser(ctx, userID, models.UpdateUserInput{Password: &password}); err != nil {
		return models.User{}, fmt.Errorf("tenancy: reset member password: %w", err)
	}
	if s.sessions != nil {
		if err := s.sessions.RevokeAllByUser(ctx, userID); err != nil {
			return models.User{}, fmt.Errorf("tenancy: revoke reset member sessions: %w", err)
		}
	}
	return s.Get(ctx, tenantID, userID)
}

// Delete removes the native account after resolving membership in the URL
// tenant. If it owned the organization, ownership is transferred first so the
// existing RESTRICT foreign key cannot turn a scoped delete into a 500.
func (s *MemberService) Delete(ctx context.Context, tenantID uuid.UUID, userID int) error {
	if err := s.RequireMembership(ctx, tenantID, userID); err != nil {
		return err
	}
	var ownerID *int
	if err := s.pool.QueryRow(ctx, `SELECT owner_account_id FROM organizations WHERE id = $1`, tenantID).Scan(&ownerID); err != nil {
		return fmt.Errorf("tenancy: load member tenant owner: %w", err)
	}
	if ownerID != nil && *ownerID == userID {
		var replacement int
		err := s.pool.QueryRow(ctx, `
			SELECT account_id FROM organization_memberships
			WHERE organization_id = $1 AND account_id <> $2
			ORDER BY account_id LIMIT 1`, tenantID, userID).Scan(&replacement)
		var replacementArg any
		status := OrganizationInitializing
		if err == nil {
			replacementArg = replacement
			status = OrganizationActive
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("tenancy: choose replacement owner: %w", err)
		}
		if _, err := s.pool.Exec(ctx, `
			UPDATE organizations SET owner_account_id = $2, status = $3, updated_at = now()
			WHERE id = $1`, tenantID, replacementArg, status); err != nil {
			return fmt.Errorf("tenancy: transfer member tenant ownership: %w", err)
		}
	}
	if err := s.accounts.DeleteUser(ctx, userID); err != nil {
		return fmt.Errorf("tenancy: delete member: %w", err)
	}
	s.store.invalidateTenantLimitsCache()
	return nil
}
