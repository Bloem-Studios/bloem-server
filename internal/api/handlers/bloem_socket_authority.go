package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type bloemSocketSessions interface {
	eventsSessionValidator
	GetByID(context.Context, string) (*models.AuthSession, error)
}

type bloemSocketProfiles interface {
	ProfileOrganization(context.Context, int, string) (uuid.UUID, error)
}

type bloemSocketBinding struct {
	Version                 int             `json:"version"`
	Principal               auth.Claims     `json:"principal"`
	Tenant                  tenancy.Context `json:"tenant"`
	ImpersonatorIncarnation uuid.UUID       `json:"impersonator_incarnation"`
	// DeviceID is the login session's device, captured at delegation.
	DeviceID string `json:"device_id,omitempty"`
}

// bloemSocketAuthority preserves the delegated principal for contextless
// handshakes without selecting a new tenant from a bare account/profile.
type bloemSocketAuthority struct {
	sessions bloemSocketSessions
	users    access.UserRepository
	tenants  apimw.TenantResolver
	profiles bloemSocketProfiles
	base     EventsSocketValidator
}

func (h *bloemSocketAuthority) bind(ctx context.Context, identity evt.SocketIdentity) (evt.SocketIdentity, error) {
	if h == nil || h.base == nil || h.sessions == nil || h.users == nil || h.tenants == nil || h.profiles == nil {
		return evt.SocketIdentity{}, evt.ErrSocketTicket
	}
	claims := apimw.GetClaims(ctx)
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || claims == nil || apimw.GetProfileID(ctx) != identity.ProfileID || !bloemSocketPrincipalMatches(*claims, identity) {
		return evt.SocketIdentity{}, evt.ErrSocketTicket
	}
	binding := bloemSocketBinding{Version: 1, Principal: *claims, Tenant: tenant}
	// Account/OAuth JWTs carry no device, while session creation records the
	// client's device. Capture that server-held value once at delegation;
	// revalidation still requires an exact match.
	if tenant.Legacy {
		checkCtx, stop := context.WithTimeout(ctx, 2*time.Second)
		session, err := bloemSocketSession(checkCtx, h.sessions, identity.SessionID)
		stop()
		if err != nil {
			return evt.SocketIdentity{}, err
		}
		binding.DeviceID = session.DeviceID
	}
	if claims.ImpersonatorUserID != nil {
		checkCtx, stop := context.WithTimeout(ctx, 2*time.Second)
		defer stop()
		impersonator, err := bloemSocketAccount(checkCtx, h.users, *claims.ImpersonatorUserID)
		if err != nil {
			return evt.SocketIdentity{}, err
		}
		if impersonator.ID != *claims.ImpersonatorUserID || !impersonator.Enabled || impersonator.Role != eventsAdminRole || impersonator.AccountIncarnationID == uuid.Nil {
			return evt.SocketIdentity{}, evt.ErrSocketTicket
		}
		binding.ImpersonatorIncarnation = impersonator.AccountIncarnationID
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		return evt.SocketIdentity{}, evt.ErrSocketTicket
	}
	identity.AuthorityBinding = string(encoded)
	return identity, nil
}

func (h *bloemSocketAuthority) validate(ctx context.Context, identity evt.SocketIdentity) (context.Context, *auth.Claims, error) {
	if h == nil || h.base == nil || h.tenants == nil || h.profiles == nil {
		return ctx, nil, evt.ErrSocketTicket
	}
	if identity.APIKeyID != 0 {
		return h.validateKey(ctx, identity)
	}
	var binding bloemSocketBinding
	if json.Unmarshal([]byte(identity.AuthorityBinding), &binding) != nil || binding.Version != 1 || !bloemSocketPrincipalMatches(binding.Principal, identity) {
		return ctx, nil, evt.ErrSocketTicket
	}
	checkCtx, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	principal := binding.Principal
	session, err := bloemSocketSession(checkCtx, h.sessions, identity.SessionID)
	if err != nil {
		return ctx, nil, err
	}
	if session.ID != identity.SessionID || session.UserID != identity.UserID || session.RevokedAt != nil || !session.ExpiresAt.After(time.Now()) ||
		session.AuthMethod != "account" || session.DeviceID != binding.DeviceID || !bloemSocketSameImpersonator(session.ImpersonatorUserID, principal.ImpersonatorUserID) {
		return ctx, nil, evt.ErrSocketTicket
	}
	user, err := bloemSocketAccount(checkCtx, h.users, identity.UserID)
	if err != nil {
		return ctx, nil, err
	}
	if user.ID != identity.UserID || user.AccountIncarnationID == uuid.Nil || user.AccountIncarnationID.String() != principal.AccountIncarnationID {
		return ctx, nil, evt.ErrSocketTicket
	}
	if principal.ImpersonatorUserID != nil {
		impersonator, err := bloemSocketAccount(checkCtx, h.users, *principal.ImpersonatorUserID)
		if err != nil {
			return ctx, nil, err
		}
		if impersonator.ID != *principal.ImpersonatorUserID || !impersonator.Enabled || impersonator.Role != eventsAdminRole || binding.ImpersonatorIncarnation == uuid.Nil || impersonator.AccountIncarnationID != binding.ImpersonatorIncarnation {
			return ctx, nil, evt.ErrSocketTicket
		}
	}
	tenant := binding.Tenant
	if tenant.AccountID != identity.UserID || tenant.OrganizationID == uuid.Nil || tenant.MembershipID == uuid.Nil || tenant.PolicyRevision <= 0 || tenant.SecurityRevision <= 0 {
		return ctx, nil, evt.ErrSocketTicket
	}
	if !tenant.Legacy || !tenant.OrganizationDefault {
		return ctx, nil, evt.ErrSocketTicket
	}
	fresh, err := h.tenants.Resolve(checkCtx, identity.UserID, &tenant.OrganizationID, tenant.Legacy)
	if err != nil {
		return ctx, nil, bloemSocketTenantError(err)
	}
	if fresh != tenant {
		return ctx, nil, evt.ErrSocketTicket
	}
	if err := h.profileInOrganization(checkCtx, identity, tenant.OrganizationID); err != nil {
		return ctx, nil, err
	}
	validated, effective, err := h.base(tenancy.WithContext(ctx, fresh), identity)
	if err != nil {
		return ctx, nil, err
	}
	scope, ok := access.GetScope(validated)
	if !ok || scope.UserID != identity.UserID || scope.ProfileID != identity.ProfileID {
		return ctx, nil, evt.ErrSocketTicket
	}
	// Preserve the authenticated principal; the shared validator may only
	// reduce its role for a secondary household profile.
	principal.Role = effective.Role
	validated = apimw.SetClaims(validated, &principal)
	return validated, &principal, nil
}

// validateKey admits an API key as an HTTP request with that key is admitted:
// Silo's key checks decide it, in the tenant its owner's requests resolve to.
// A key holds no login session to bind, so its ticket carries no Bloem
// binding, and one that does was not minted for a key. A user key's ticket
// may name the profile the key acts as (Silo's profile keys, or an account
// key sent with X-Profile-Id); that profile must belong to the same
// organization, as it must for a sign-in ticket.
func (h *bloemSocketAuthority) validateKey(ctx context.Context, identity evt.SocketIdentity) (context.Context, *auth.Claims, error) {
	if identity.AuthorityBinding != "" || identity.SessionID != "" {
		return ctx, nil, evt.ErrSocketTicket
	}
	checkCtx, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	tenant, err := h.tenants.Resolve(checkCtx, identity.UserID, nil, true)
	if err != nil {
		return ctx, nil, bloemSocketTenantError(err)
	}
	if err := h.profileInOrganization(checkCtx, identity, tenant.OrganizationID); err != nil {
		return ctx, nil, err
	}
	return h.base(tenancy.WithContext(ctx, tenant), identity)
}

// profileInOrganization checks that the identity's profile, when it names
// one, belongs to organization.
func (h *bloemSocketAuthority) profileInOrganization(ctx context.Context, identity evt.SocketIdentity, organization uuid.UUID) error {
	if identity.ProfileID == "" {
		return nil
	}
	current, err := h.profiles.ProfileOrganization(ctx, identity.UserID, identity.ProfileID)
	if err != nil {
		return bloemSocketTenantError(err)
	}
	if current == uuid.Nil || current != organization {
		return evt.ErrSocketTicket
	}
	return nil
}

// The reads below follow Silo's rule for a socket check: a read that answers
// the credential is gone gives evt.ErrSocketTicket (credential_invalid, 4002),
// and a read that fails or times out gives an unavailable error
// (authority_unavailable, 4004; 503 at mint and upgrade), so the client
// retries rather than drop a credential that may still hold.

// bloemSocketSession reads the login session.
func bloemSocketSession(ctx context.Context, sessions bloemSocketSessions, id string) (*models.AuthSession, error) {
	session, err := sessions.GetByID(ctx, id)
	if auth.IsSessionNotFound(err) || (err == nil && session == nil) {
		return nil, evt.ErrSocketTicket
	}
	if err != nil {
		return nil, socketAuthorityUnavailable(err)
	}
	return session, nil
}

// bloemSocketAccount reads an account; the caller judges whether it may act.
func bloemSocketAccount(ctx context.Context, users access.UserRepository, id int) (*models.User, error) {
	user, err := users.GetByID(ctx, id)
	if errors.Is(err, auth.ErrNotFound) || (err == nil && user == nil) {
		return nil, evt.ErrSocketTicket
	}
	if err != nil {
		return nil, socketAuthorityUnavailable(err)
	}
	return user, nil
}

// bloemSocketTenantError classifies a failed tenant or profile-organization
// read as the tenant middleware does for an HTTP request: a hidden, suspended
// or unresolved tenant ends the credential, anything else could not be
// checked.
func bloemSocketTenantError(err error) error {
	switch {
	case errors.Is(err, tenancy.ErrTenantNotFoundOrHidden), errors.Is(err, tenancy.ErrTenantSuspended), errors.Is(err, tenancy.ErrOwnershipResolutionRequired):
		return evt.ErrSocketTicket
	}
	return socketAuthorityUnavailable(err)
}

func bloemSocketPrincipalMatches(claims auth.Claims, identity evt.SocketIdentity) bool {
	// Only an account access token delegates; a preview guest or a
	// profile-bound credential does not.
	return claims.TokenType == auth.TokenTypeAccess && (claims.AuthMethod == "" || claims.AuthMethod == "account") &&
		claims.ProfileID == "" && claims.APIKeyID == 0 && len(claims.APIKeyScopes) == 0 &&
		claims.UserID > 0 && claims.UserID == identity.UserID && claims.SessionID != "" && claims.SessionID == identity.SessionID && claims.Role == identity.Role &&
		claims.AccountIncarnationID != "" && claims.ExpiresAt != nil && claims.ExpiresAt.Equal(identity.AccessExpiresAt) && identity.AccessExpiresAt.After(time.Now()) &&
		bloemSocketSameImpersonator(claims.ImpersonatorUserID, identity.ImpersonatorUserID) &&
		(claims.ImpersonatorUserID == nil || (*claims.ImpersonatorUserID > 0 && *claims.ImpersonatorUserID != claims.UserID))
}

func bloemSocketSameImpersonator(a, b *int) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
