package handlers

// Bloem-owned adapter for the contextless Silo events handshake. A fresh
// subject lookup alone could move a delegated credential to a different tenant;
// resolve the captured organization and compare its authority instead.

import (
	"context"
	"encoding/json"
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
}

// BloemEventsSocketV2 keeps the shared protocol, ticket store, PIN verification,
// access fingerprint and revocation loop while restoring Bloem authority.
type BloemEventsSocketV2 struct {
	*EventsSocketV2
	sessions bloemSocketSessions
	users    access.UserRepository
	tenants  apimw.TenantResolver
	profiles bloemSocketProfiles
	base     EventsSocketValidator
}

func NewBloemEventsSocketV2(events *EventsHandler, tickets *evt.SocketTicketStore, sessions bloemSocketSessions, users access.UserRepository, resolver apimw.ViewerResolver, primary apimw.PrimaryProfileChecker, tenants apimw.TenantResolver, profiles bloemSocketProfiles, publicURL string) *BloemEventsSocketV2 {
	socket := NewEventsSocketV2(events, tickets, sessions, users, resolver, primary, publicURL)
	h := &BloemEventsSocketV2{EventsSocketV2: socket, sessions: sessions, users: users, tenants: tenants, profiles: profiles, base: socket.Validate}
	socket.Validate = h.validate
	return h
}

func (h *BloemEventsSocketV2) Mint(ctx context.Context, identity evt.SocketIdentity) (string, error) {
	if h == nil || h.EventsSocketV2 == nil || h.base == nil || h.tenants == nil || h.profiles == nil {
		return "", evt.ErrSocketTicket
	}
	claims := apimw.GetClaims(ctx)
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || claims == nil || apimw.GetProfileID(ctx) != identity.ProfileID || !bloemSocketPrincipalMatches(*claims, identity) {
		return "", evt.ErrSocketTicket
	}
	binding := bloemSocketBinding{Version: 1, Principal: *claims, Tenant: tenant}
	if claims.ImpersonatorUserID != nil {
		checkCtx, stop := context.WithTimeout(ctx, 2*time.Second)
		defer stop()
		impersonator, err := h.users.GetByID(checkCtx, *claims.ImpersonatorUserID)
		if err != nil || impersonator == nil || impersonator.ID != *claims.ImpersonatorUserID || !impersonator.Enabled || impersonator.Role != eventsAdminRole || impersonator.AccountIncarnationID == uuid.Nil {
			return "", evt.ErrSocketTicket
		}
		binding.ImpersonatorIncarnation = impersonator.AccountIncarnationID
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		return "", evt.ErrSocketTicket
	}
	identity.AuthorityBinding = string(encoded)
	return h.EventsSocketV2.Mint(ctx, identity)
}

func (h *BloemEventsSocketV2) validate(ctx context.Context, identity evt.SocketIdentity) (context.Context, *auth.Claims, error) {
	if h == nil || h.base == nil || h.tenants == nil || h.profiles == nil {
		return ctx, nil, evt.ErrSocketTicket
	}
	var binding bloemSocketBinding
	if json.Unmarshal([]byte(identity.AuthorityBinding), &binding) != nil || binding.Version != 1 || !bloemSocketPrincipalMatches(binding.Principal, identity) {
		return ctx, nil, evt.ErrSocketTicket
	}
	checkCtx, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	principal := binding.Principal
	session, err := h.sessions.GetByID(checkCtx, identity.SessionID)
	if err != nil || session == nil || session.ID != identity.SessionID || session.UserID != identity.UserID || session.RevokedAt != nil || !session.ExpiresAt.After(time.Now()) ||
		session.AuthMethod != "account" || session.ProfileID != nil || session.ProfileCredentialRevision != nil || session.DeviceID != principal.DeviceID || !bloemSocketSameImpersonator(session.ImpersonatorUserID, principal.ImpersonatorUserID) {
		return ctx, nil, evt.ErrSocketTicket
	}
	user, err := h.users.GetByID(checkCtx, identity.UserID)
	if err != nil || user == nil || user.ID != identity.UserID || user.AccountIncarnationID == uuid.Nil || user.AccountIncarnationID.String() != principal.AccountIncarnationID {
		return ctx, nil, evt.ErrSocketTicket
	}
	if principal.ImpersonatorUserID != nil {
		impersonator, err := h.users.GetByID(checkCtx, *principal.ImpersonatorUserID)
		if err != nil || impersonator == nil || impersonator.ID != *principal.ImpersonatorUserID || !impersonator.Enabled || impersonator.Role != eventsAdminRole || binding.ImpersonatorIncarnation == uuid.Nil || impersonator.AccountIncarnationID != binding.ImpersonatorIncarnation {
			return ctx, nil, evt.ErrSocketTicket
		}
	}
	tenant := binding.Tenant
	if tenant.AccountID != identity.UserID || tenant.OrganizationID == uuid.Nil || tenant.MembershipID == uuid.Nil || tenant.PolicyRevision <= 0 || tenant.SecurityRevision <= 0 {
		return ctx, nil, evt.ErrSocketTicket
	}
	// Partial or stale credential bindings must not become a legacy selection.
	if principal.OrganizationID != "" || principal.MembershipID != "" || principal.PolicyRevision != 0 || principal.SecurityRevision != 0 {
		if principal.OrganizationID != tenant.OrganizationID.String() || principal.MembershipID != tenant.MembershipID.String() || principal.PolicyRevision != tenant.PolicyRevision || principal.SecurityRevision != tenant.SecurityRevision || tenant.Legacy {
			return ctx, nil, evt.ErrSocketTicket
		}
	} else if !tenant.Legacy || !tenant.OrganizationDefault {
		return ctx, nil, evt.ErrSocketTicket
	}
	fresh, err := h.tenants.Resolve(checkCtx, identity.UserID, &tenant.OrganizationID, tenant.Legacy)
	if err != nil || fresh != tenant {
		return ctx, nil, evt.ErrSocketTicket
	}
	if identity.ProfileID != "" {
		organization, err := h.profiles.ProfileOrganization(checkCtx, identity.UserID, identity.ProfileID)
		if err != nil || organization != tenant.OrganizationID {
			return ctx, nil, evt.ErrSocketTicket
		}
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

func bloemSocketPrincipalMatches(claims auth.Claims, identity evt.SocketIdentity) bool {
	// V2 events is outside the direct-profile allowlist. Refuse it here too,
	// including a stored ticket whose original credential was profile-bound.
	return claims.TokenType == auth.TokenTypeAccess && (claims.AuthMethod == "" || claims.AuthMethod == "account") &&
		claims.ProfileID == "" && claims.CredentialRevision == 0 && claims.APIKeyID == 0 && len(claims.APIKeyScopes) == 0 &&
		claims.UserID > 0 && claims.UserID == identity.UserID && claims.SessionID != "" && claims.SessionID == identity.SessionID && claims.Role == identity.Role &&
		claims.AccountIncarnationID != "" && claims.ExpiresAt != nil && claims.ExpiresAt.Time.Equal(identity.AccessExpiresAt) && identity.AccessExpiresAt.After(time.Now()) &&
		bloemSocketSameImpersonator(claims.ImpersonatorUserID, identity.ImpersonatorUserID) &&
		(claims.ImpersonatorUserID == nil || (*claims.ImpersonatorUserID > 0 && *claims.ImpersonatorUserID != claims.UserID))
}

func bloemSocketSameImpersonator(a, b *int) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
