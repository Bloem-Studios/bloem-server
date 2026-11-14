// Package supportaccess is break-glass access for the platform operator.
//
// A platform administrator who is not a member of a customer's organization
// can open it only by stating a reason. Every opening is a row in
// bloem_support_access and an audit event the organization's administrators
// read in Activity. The access is capped at four hours, ends with the login
// session it was opened from, and is revalidated on every request that uses
// it. For now only the server Owner may open one (the platform owner's
// decision, 2026-11-13); widening that is a deliberate change here.
package supportaccess

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
)

const (
	// MaxDuration is the hard cap from opening; a longer visit needs a new reason.
	MaxDuration = 4 * time.Hour
	// MinReasonLength and MaxReasonLength bound the reason an operator states.
	MinReasonLength = 10
	MaxReasonLength = 500
	// openLimit is the most openings one operator may make per hour.
	openLimit  = 20
	openWindow = time.Hour

	actionOpened = "support_access.opened"
	actionClosed = "support_access.closed"
)

var (
	// ErrNotEligible hides every reason an account may not open support access
	// (not an administrator, not the Owner, disabled). Callers answer as they
	// would for an organization the caller cannot see.
	ErrNotEligible = errors.New("account may not open support access")
	// ErrNotFound is an access that is unknown, closed, expired, belongs to
	// another operator, login session or organization, or whose organization
	// or operator no longer qualifies. One answer for all.
	ErrNotFound = errors.New("support access not found")
	// ErrOrganizationUnavailable is an organization that does not exist, is not
	// active or has no owner.
	ErrOrganizationUnavailable = errors.New("organization is unavailable for support access")
	// ErrInvalidReason is a reason that is too short, too long or has control characters.
	ErrInvalidReason = errors.New("invalid support access reason")
	// ErrRateLimited is matched for every *RateLimitedError.
	ErrRateLimited = errors.New("too many support accesses opened")
)

// RateLimitedError says when the operator may open again.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string { return ErrRateLimited.Error() }
func (e *RateLimitedError) Is(target error) bool {
	return target == ErrRateLimited
}

// Access is one opening.
type Access struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	ActorAccountID int
	LoginSession   string
	Reason         string
	OpenedAt       time.Time
	ExpiresAt      time.Time
	ClosedAt       *time.Time
}

// ValidateReason trims and checks the reason an operator states.
func ValidateReason(raw string) (string, error) {
	reason := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(reason); n < MinReasonLength || n > MaxReasonLength {
		return "", ErrInvalidReason
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return "", ErrInvalidReason
		}
	}
	return reason, nil
}

// Store keeps the support access records.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a PostgreSQL-backed store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Eligible reports whether the account may open support access: an enabled
// platform administrator who is the server Owner.
func (s *Store) Eligible(ctx context.Context, accountID int) (bool, error) {
	var eligible bool
	err := s.pool.QueryRow(ctx, `SELECT enabled AND role = 'admin' AND is_owner FROM users WHERE id = $1`, accountID).Scan(&eligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return eligible, err
}

// OpenInput is one opening.
type OpenInput struct {
	OrganizationID uuid.UUID
	ActorAccountID int
	LoginSessionID string
	Reason         string
	RequestID      string
}

// Open records a new support access and its audit event in one transaction.
// The reason must already have passed ValidateReason.
func (s *Store) Open(ctx context.Context, in OpenInput) (Access, error) {
	return adminpeople.RunSupportTx(ctx, s.pool.Begin, func(tx pgx.Tx) (Access, error) {
		var zero Access
		var eligible bool
		err := tx.QueryRow(ctx, `SELECT enabled AND role = 'admin' AND is_owner FROM users WHERE id = $1 FOR SHARE`, in.ActorAccountID).Scan(&eligible)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !eligible) {
			return zero, ErrNotEligible
		}
		if err != nil {
			return zero, err
		}
		var active bool
		err = tx.QueryRow(ctx, `SELECT status = 'active' AND owner_account_id IS NOT NULL FROM organizations WHERE id = $1 FOR SHARE`, in.OrganizationID).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
			return zero, ErrOrganizationUnavailable
		}
		if err != nil {
			return zero, err
		}

		// Serialize this operator's openings so the hourly budget cannot be
		// passed by parallel requests.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, fmt.Sprintf("bloem:support_open:%d", in.ActorAccountID)); err != nil {
			return zero, err
		}
		var count int
		var oldest *time.Time
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT count(*), min(opened_at), now() FROM bloem_support_access WHERE actor_account_id = $1 AND opened_at > now() - make_interval(secs => $2)`, in.ActorAccountID, openWindow.Seconds()).Scan(&count, &oldest, &now); err != nil {
			return zero, err
		}
		if count >= openLimit && oldest != nil {
			wait := oldest.Add(openWindow).Sub(now)
			if wait < time.Second {
				wait = time.Second
			}
			return zero, &RateLimitedError{RetryAfter: wait}
		}

		var access Access
		err = tx.QueryRow(ctx, `
			INSERT INTO bloem_support_access (organization_id, actor_account_id, login_session_id, reason, opened_at, expires_at)
			VALUES ($1, $2, $3, $4, now(), now() + make_interval(secs => $5))
			RETURNING id, organization_id, actor_account_id, login_session_id, reason, opened_at, expires_at`,
			in.OrganizationID, in.ActorAccountID, in.LoginSessionID, in.Reason, MaxDuration.Seconds()).
			Scan(&access.ID, &access.OrganizationID, &access.ActorAccountID, &access.LoginSession, &access.Reason, &access.OpenedAt, &access.ExpiresAt)
		if err != nil {
			return zero, fmt.Errorf("record support access: %w", err)
		}
		if err := adminpeople.RecordAudit(ctx, tx, adminpeople.AuditEvent{
			Actor:  adminpeople.MutationActor{AccountID: in.ActorAccountID, Authority: adminpeople.AuthorityPlatformAdmin, RequestID: in.RequestID, SupportAccessID: access.ID},
			Action: actionOpened, TargetType: "support_access", TargetID: access.ID.String(), OrganizationID: in.OrganizationID, Outcome: "success",
			After: map[string]any{"reason": access.Reason, "expires_at": access.ExpiresAt.UTC().Format(time.RFC3339)},
		}); err != nil {
			return zero, err
		}
		return access, nil
	})
}

// Validate confirms an access is open for this operator, login session and
// organization, and that the operator and organization still qualify. It does
// not extend the access: the cap runs from opening. It returns the access and
// the organization's current policy revision, which a pass is bound to.
func (s *Store) Validate(ctx context.Context, accessID, organizationID uuid.UUID, accountID int, loginSessionID string) (Access, int64, error) {
	return s.validate(ctx, s.pool, accessID, organizationID, accountID, loginSessionID, "")
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Store) validate(ctx context.Context, q queryer, accessID, organizationID uuid.UUID, accountID int, loginSessionID, lock string) (Access, int64, error) {
	var access Access
	var policyRevision int64
	var orgActive, actorEligible bool
	err := q.QueryRow(ctx, `
		SELECT a.id, a.organization_id, a.actor_account_id, a.login_session_id, a.reason, a.opened_at, a.expires_at, a.closed_at,
		       o.policy_revision, o.status = 'active' AND o.owner_account_id IS NOT NULL, u.enabled AND u.role = 'admin' AND u.is_owner
		FROM bloem_support_access a
		JOIN organizations o ON o.id = a.organization_id
		JOIN users u ON u.id = a.actor_account_id
		WHERE a.id = $1 AND a.organization_id = $2 AND a.actor_account_id = $3 AND a.login_session_id = $4
		  AND a.closed_at IS NULL AND a.expires_at > now()`+lock,
		accessID, organizationID, accountID, loginSessionID).
		Scan(&access.ID, &access.OrganizationID, &access.ActorAccountID, &access.LoginSession, &access.Reason, &access.OpenedAt, &access.ExpiresAt, &access.ClosedAt,
			&policyRevision, &orgActive, &actorEligible)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!orgActive || !actorEligible)) {
		return Access{}, 0, ErrNotFound
	}
	if err != nil {
		return Access{}, 0, err
	}
	return access, policyRevision, nil
}

// ValidateSupportAccess is Validate for the administrative context middleware.
func (s *Store) ValidateSupportAccess(ctx context.Context, accessID, organizationID uuid.UUID, accountID int, loginSessionID string) (int64, error) {
	_, policyRevision, err := s.Validate(ctx, accessID, organizationID, accountID, loginSessionID)
	return policyRevision, err
}

// Close ends an access before its time and records it. Closing an access that
// is already closed or expired is ErrNotFound.
func (s *Store) Close(ctx context.Context, accessID, organizationID uuid.UUID, accountID int, loginSessionID, requestID string) error {
	_, err := adminpeople.RunSupportTx(ctx, s.pool.Begin, func(tx pgx.Tx) (struct{}, error) {
		access, _, err := s.validate(ctx, tx, accessID, organizationID, accountID, loginSessionID, " FOR UPDATE OF a")
		if err != nil {
			return struct{}{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE bloem_support_access SET closed_at = now() WHERE id = $1`, access.ID); err != nil {
			return struct{}{}, fmt.Errorf("close support access: %w", err)
		}
		return struct{}{}, adminpeople.RecordAudit(ctx, tx, adminpeople.AuditEvent{
			Actor:  adminpeople.MutationActor{AccountID: accountID, Authority: adminpeople.AuthorityPlatformAdmin, RequestID: requestID, SupportAccessID: access.ID},
			Action: actionClosed, TargetType: "support_access", TargetID: access.ID.String(), OrganizationID: organizationID, Outcome: "success",
		})
	})
	return err
}
