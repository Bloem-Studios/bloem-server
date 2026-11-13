package handlers

// Bloem-owned. The routes an organization administrator uses to give support
// to their own organization: reset a member's password, sign a member out of
// sessions and devices, edit a member's profiles, watch and stop the
// organization's streams.
//
// Every route here:
//   - takes the organization from the administrative context pass, never from
//     the request (a stray organization_id in a body is a 400);
//   - judges who may act on whom with adminpeople.LockAuthority, under row
//     locks in the same transaction as the write;
//   - answers a person who is protected from the actor with 409
//     target_protected, never 403, because clients read a 403 on an
//     organization route as loss of access to the organization;
//   - answers an id outside the organization exactly as an id that does not
//     exist: 404 not_found;
//   - writes its own admin_audit_events rows, because Silo's audit does not
//     record a request under /api/bloem/v2.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/mail"
	"github.com/Silo-Server/silo-server/internal/passwordreset"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// BloemOrganizationSupportHandler serves the organization support routes.
type BloemOrganizationSupportHandler struct {
	pool   *pgxpool.Pool
	people *adminpeople.Service
	resets *passwordreset.Service

	// admin supplies the household profile lifecycle, the device registry and
	// the session invalidation hooks Silo's own admin routes use. Nil in tests
	// that only exercise reads.
	admin *AdminHandler

	// SessionsRevoked and ProfileSessionsRevoked drop state held outside
	// auth_sessions (compatibility-protocol sessions) after a revoke.
	sessionsRevoked        func(ctx context.Context, userID int) error
	profileSessionsRevoked func(ctx context.Context, userID int, profileIDs []string) error

	now func() time.Time
}

// NewBloemOrganizationSupportHandler builds the handler. resets may be nil, in
// which case reset routes answer capability_not_configured.
func NewBloemOrganizationSupportHandler(pool *pgxpool.Pool, people *adminpeople.Service, resets *passwordreset.Service, admin *AdminHandler) *BloemOrganizationSupportHandler {
	h := &BloemOrganizationSupportHandler{pool: pool, people: people, resets: resets, admin: admin, now: time.Now}
	if admin != nil {
		h.sessionsRevoked = admin.BloemOnUserSessionsRevoked
		h.profileSessionsRevoked = admin.OnUserProfileSessionsRevoked
	}
	return h
}

// supportRequest is what every support route needs from the pass.
type supportRequest struct {
	Tenant tenancy.Context
	Actor  adminpeople.MutationActor
}

func (h *BloemOrganizationSupportHandler) require(w http.ResponseWriter, r *http.Request) (supportRequest, bool) {
	claims, claimsOK := middleware.GetAdminContextClaims(r.Context())
	tenant, tenantOK := tenancy.FromContext(r.Context())
	if !claimsOK || !tenantOK || claims.Scope != auth.AdminScopeOrganization || claims.AccountID <= 0 || claims.OrganizationID == uuid.Nil ||
		claims.AccountID != tenant.AccountID || claims.OrganizationID != tenant.OrganizationID || claims.MembershipID != tenant.MembershipID {
		writeError(w, http.StatusForbidden, "insufficient_organization_authority", "Organization administrator authority required")
		return supportRequest{}, false
	}
	if h == nil || h.pool == nil || h.people == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return supportRequest{}, false
	}
	authority := claims.EffectiveAuthority
	if authority == "" {
		authority = adminpeople.AuthorityOrganizationAdmin
	}
	return supportRequest{Tenant: tenant, Actor: adminpeople.MutationActor{
		AccountID: claims.AccountID, Authority: authority, MembershipID: claims.MembershipID,
		SecurityRevision: claims.SecurityRevision, PolicyRevision: claims.PolicyRevision, RequestID: adminRequestID(r),
		SupportAccessID: claims.SupportAccessID,
	}}, true
}

func supportAccountID(w http.ResponseWriter, r *http.Request) (int, bool) {
	return adminPeoplePathAccount(w, r)
}

// writeSupportError maps a support route's error. currentRevision, when it
// returns a value, is the person's current security revision for a stale
// revision answer.
func (h *BloemOrganizationSupportHandler) writeSupportError(w http.ResponseWriter, r *http.Request, err error, organizationID uuid.UUID, accountID int) {
	if writeSupportAuthorityError(w, err) {
		return
	}
	var limited *adminpeople.RateLimitedError
	switch {
	case errors.As(err, &limited):
		seconds := int(limited.RetryAfter.Round(time.Second) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many support actions; try again later")
	case errors.Is(err, adminpeople.ErrNotFound), errors.Is(err, errSupportNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
	case errors.Is(err, adminpeople.ErrAuthorizationStateChanged):
		var revision int64
		if accountID > 0 {
			if person, loadErr := h.people.Get(r.Context(), organizationID, accountID); loadErr == nil {
				revision = person.SecurityRevision
			}
		}
		writeJSON(w, http.StatusConflict, struct {
			Error           string `json:"error"`
			Message         string `json:"message"`
			CurrentRevision int64  `json:"current_revision"`
		}{"authorization_state_changed", "Authorization state changed; reload and retry", revision})
	case errors.Is(err, adminpeople.ErrMembershipNotActive):
		writeError(w, http.StatusConflict, "membership_not_active", "This person's membership is not active")
	case errors.Is(err, passwordreset.ErrAccountDisabled):
		writeError(w, http.StatusConflict, "account_disabled", "This account is disabled")
	case errors.Is(err, auth.ErrPasswordLoginDisabled), errors.Is(err, passwordreset.ErrNotEligible):
		writeError(w, http.StatusConflict, "password_login_disabled", "This account signs in through an external provider and has no password to reset")
	case errors.Is(err, passwordreset.ErrNoEmail):
		writeError(w, http.StatusConflict, "email_unavailable", "This account has no email address to send a reset link to")
	case errors.Is(err, passwordreset.ErrDeliveryUnavailable):
		capability := "link"
		if errors.Is(err, mail.ErrNotConfigured) {
			capability = "email"
		}
		writeJSON(w, http.StatusConflict, struct {
			Error      string `json:"error"`
			Message    string `json:"message"`
			Capability string `json:"capability"`
		}{"capability_not_configured", "This server cannot deliver that kind of reset link", capability})
	case errors.Is(err, errSupportConflict):
		writeError(w, http.StatusConflict, supportConflictCode(err), err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
	}
}

// errSupportNotFound and errSupportConflict let a support operation answer
// 404 and a coded 409 without a new top-level sentinel each.
var (
	errSupportNotFound = errors.New("support resource not found")
	errSupportConflict = errors.New("support conflict")
)

type supportConflict struct {
	code    string
	message string
}

func (e *supportConflict) Error() string { return e.message }
func (e *supportConflict) Is(target error) bool {
	return target == errSupportConflict
}

func supportConflictCode(err error) string {
	var conflict *supportConflict
	if errors.As(err, &conflict) {
		return conflict.code
	}
	return "conflict"
}

// supportBudgetTx opens a support transaction that first serializes and checks
// a rate-limit budget. target is the account id (as text) or the stream id.
func supportBudgetTx[T any](ctx context.Context, pool *pgxpool.Pool, budget adminpeople.Budget, organizationID uuid.UUID, actorID int, target string, fn func(pgx.Tx) (T, error)) (T, error) {
	return adminpeople.RunSupportTx(ctx, pool.Begin, func(tx pgx.Tx) (T, error) {
		var zero T
		if err := adminpeople.LockBudget(ctx, tx, budget, organizationID); err != nil {
			return zero, err
		}
		if err := adminpeople.CheckBudget(ctx, tx, budget, organizationID, actorID, target); err != nil {
			return zero, err
		}
		return fn(tx)
	})
}

func itoa(value int) string { return strconv.Itoa(value) }

func isDeadlock(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "40P01"
}
