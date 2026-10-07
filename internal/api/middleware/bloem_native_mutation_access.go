package middleware

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"slices"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// NativeMutationAccess reuses the installed host validators. It does not retain
// a policy decision through COMMIT and is unrelated to native management.
type NativeMutationAccess struct {
	auth    *AuthMiddleware
	viewer  *ViewerAccessMiddleware
	tenant  *TenantMiddleware
	users   PermissionUserLoader
	primary PrimaryProfileChecker
	policy  *PolicyPermissionMiddleware
	legacy  *PermissionMiddleware
}

func NewNativeMutationAccess(am *AuthMiddleware, vm *ViewerAccessMiddleware, tm *TenantMiddleware,
	users PermissionUserLoader, primary PrimaryProfileChecker, pm *PolicyPermissionMiddleware, lm *PermissionMiddleware) *NativeMutationAccess {
	return &NativeMutationAccess{auth: am, viewer: vm, tenant: tm, users: users, primary: primary, policy: pm, legacy: lm}
}

// NativeMutationOrigin contains only immutable original request inputs. Capture
// it after the real route gates; never substitute a destination into its URL.
type NativeMutationOrigin struct {
	request      *http.Request
	claims       auth.Claims
	input        access.ResolveInput
	nativeTenant bool
}

func CaptureNativeMutationOrigin(r *http.Request) (*NativeMutationOrigin, error) {
	claims := GetClaims(r.Context())
	scope, ok := access.GetScope(r.Context())
	if claims == nil || !ok || scope.UserID != claims.UserID {
		return nil, nativeAccessRefusal("unauthenticated")
	}
	original := r.Clone(context.WithoutCancel(r.Context()))
	original.Body = nil
	original.Header = make(http.Header)
	for _, key := range []string{"Authorization", "X-Profile-Id", "X-Profile-Token", siloDeviceIDHeader} {
		original.Header.Set(key, r.Header.Get(key))
	}
	if claims.AuthMethod == auth.AuthMethodDirectProfile {
		original.Header.Set(siloDeviceIDHeader, claims.DeviceID)
	}
	// Use the scope's actual bound profile (including direct-profile resolution),
	// while the role gate retains the original declared-profile semantics.
	copied := *claims
	copied.APIKeyScopes = slices.Clone(claims.APIKeyScopes)
	copied.Audience = slices.Clone(claims.Audience)
	if claims.ImpersonatorUserID != nil {
		id := *claims.ImpersonatorUserID
		copied.ImpersonatorUserID = &id
	}
	return &NativeMutationOrigin{request: original, claims: copied, nativeTenant: strings.HasPrefix(original.URL.Path, "/api/v2/"), input: access.ResolveInput{
		UserID: claims.UserID, SessionID: claims.SessionID, ProfileID: scope.ProfileID,
		ProfileToken:        r.Header.Get("X-Profile-Token"),
		SkipPINVerification: claims.TokenType == auth.TokenTypeAPIKey || claims.TokenType == auth.TokenTypeApplePushDisplay || claims.AuthMethod == auth.AuthMethodDirectProfile || IsAudienceTicketAuthorized(r.Context()),
	}}, nil
}

// Current repeats credential, current role, tenant and installed viewer/PIN
// checks, then the owning role/curation decision for the COMPLETE library set.
func (m *NativeMutationAccess) Current(ctx context.Context, o *NativeMutationOrigin, gate string, libraries []int) (context.Context, error) {
	if m == nil || o == nil || m.auth == nil || m.viewer == nil || m.tenant == nil || m.users == nil {
		return ctx, nativeAccessRefusal("native_storage_unavailable")
	}
	// Preserve the authenticated origin's device/IP facts. The actual phase
	// context supplies cancellation/deadline (the request, or the owning
	// continuation's existing finite bound). No credential lifetime is invented.
	checkCtx, cancel := context.WithCancel(o.request.Context())
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	if err := m.auth.RevalidateCurrent(checkCtx, o.request.Header.Get("Authorization"), &o.claims, o.request.Method, o.request.URL.Path); err != nil {
		if errors.Is(err, ErrCurrentCredentialInvalid) {
			return ctx, nativeAccessRefusal("unauthenticated")
		}
		if errors.Is(err, ErrCurrentCredentialForbidden) {
			return ctx, nativeAccessRefusal("forbidden")
		}
		return ctx, nativeAccessRefusal("native_storage_unavailable")
	}
	user, err := m.users.GetByID(checkCtx, o.claims.UserID)
	if err != nil {
		return ctx, nativeAccessRefusal("native_storage_unavailable")
	}
	if user == nil || !user.Enabled {
		return ctx, nativeAccessRefusal("unauthenticated")
	}
	claims := o.claims
	if claims.TokenType == auth.TokenTypeAPIKey {
		claims.Role = user.Role
	} else {
		role, active, err := m.auth.sessionValidator.ActiveSessionRole(checkCtx, claims.SessionID)
		if err != nil {
			return ctx, nativeAccessRefusal("native_storage_unavailable")
		}
		if !active || role != claims.Role {
			return ctx, nativeAccessRefusal("unauthenticated")
		}
	}
	checkCtx = SetClaims(checkCtx, &claims)
	r := o.request.WithContext(checkCtx)
	tenant, err := m.tenant.nativeMutationTenant(checkCtx, &claims, o.nativeTenant)
	if err != nil {
		return ctx, err
	}
	checkCtx = tenancy.WithContext(checkCtx, tenant)
	scope, err := m.viewer.ResolveCurrent(checkCtx, o.input)
	if err != nil {
		if errors.Is(err, access.ErrProfileUnverified) {
			return ctx, nativeAccessRefusal("forbidden")
		}
		if errors.Is(err, access.ErrProfileNotFound) {
			return ctx, nativeAccessRefusal("not_found")
		}
		return ctx, nativeAccessRefusal("native_storage_unavailable")
	}
	checkCtx = access.SetScope(checkCtx, scope)
	r = r.WithContext(checkCtx)
	if err = m.requireTargets(r, gate, libraries); err != nil {
		return ctx, err
	}
	// The fresh scope is for this phase only. The delegate still receives the
	// original context/args. Producers call Current again after reselection.
	current := SetClaims(ctx, &claims)
	tenant, _ = tenancy.FromContext(checkCtx)
	current = tenancy.WithContext(current, tenant)
	current = SetProfileID(current, scope.ProfileID)
	return access.SetScope(current, scope), nil
}

func nativeAccessRefusal(code string) error { return &catalog.NativePhaseRefusal{Code: code} }

// nativeMutationTenant follows ResolveLegacy for v1 and ResolveNative for v2.
// Account tenant claims do not select a tenant on the legacy surface.
func (m *TenantMiddleware) nativeMutationTenant(ctx context.Context, claims *auth.Claims, native bool) (tenancy.Context, error) {
	bound := claims.AuthMethod == auth.AuthMethodDirectProfile
	if native {
		bound = bound || claims.OrganizationID != "" || claims.MembershipID != "" || claims.PolicyRevision != 0 || claims.SecurityRevision != 0
	}
	if bound {
		return m.nativeMutationBoundTenant(ctx, claims)
	}
	current, err := m.resolve(ctx, claims.UserID, nil, true)
	if err != nil {
		if errors.Is(err, tenancy.ErrTenantSuspended) {
			return current, nativeAccessRefusal("forbidden")
		}
		if errors.Is(err, tenancy.ErrTenantNotFoundOrHidden) || errors.Is(err, tenancy.ErrOwnershipResolutionRequired) {
			return current, nativeAccessRefusal("unauthenticated")
		}
		return current, nativeAccessRefusal("native_storage_unavailable")
	}
	return current, nil
}

// This owning companion uses resolve and repeats the same bound-token
// comparison as resolveBoundTenant. It has no destination HTTP request.
func (m *TenantMiddleware) nativeMutationBoundTenant(ctx context.Context, claims *auth.Claims) (tenancy.Context, error) {
	if claims.PolicyRevision <= 0 || claims.SecurityRevision <= 0 {
		return tenancy.Context{}, nativeAccessRefusal("unauthenticated")
	}
	organization, err := uuid.Parse(claims.OrganizationID)
	if err != nil {
		return tenancy.Context{}, nativeAccessRefusal("unauthenticated")
	}
	membership, err := uuid.Parse(claims.MembershipID)
	if err != nil {
		return tenancy.Context{}, nativeAccessRefusal("unauthenticated")
	}
	current, err := m.resolve(ctx, claims.UserID, &organization, false)
	if err != nil {
		if errors.Is(err, tenancy.ErrTenantSuspended) {
			return current, nativeAccessRefusal("forbidden")
		}
		if errors.Is(err, tenancy.ErrTenantNotFoundOrHidden) || errors.Is(err, tenancy.ErrOwnershipResolutionRequired) {
			return current, nativeAccessRefusal("unauthenticated")
		}
		return current, nativeAccessRefusal("native_storage_unavailable")
	}
	if current.AccountID != claims.UserID || current.OrganizationID != organization || current.MembershipID != membership || current.PolicyRevision != claims.PolicyRevision || current.SecurityRevision != claims.SecurityRevision {
		return current, nativeAccessRefusal("unauthenticated")
	}
	return current, nil
}

func (m *NativeMutationAccess) requireTargets(r *http.Request, gate string, libraries []int) error {
	if gate == "viewer" {
		return nil
	}
	claims := GetClaims(r.Context())
	if claims == nil {
		return nativeAccessRefusal("unauthenticated")
	}
	if m.policy != nil {
		profile, primary, err := resolveActingAdminFacts(r, claims.UserID, m.primary)
		if err != nil {
			return nativeAccessRefusal("native_storage_unavailable")
		}
		if claims.Role == "admin" {
			decision, err := m.policy.checkPermission(r, policy.PermissionInput{UserID: claims.UserID, Role: claims.Role, UserEnabled: true,
				Permission: policy.PermissionActingAdmin, DeclaredProfileID: profile, ActingAsPrimary: primary})
			if err != nil {
				return nativeAccessRefusal("native_storage_unavailable")
			}
			if decision.Allowed {
				return nil
			}
		}
		if gate == "admin" {
			return nativeAccessRefusal("forbidden")
		}
		user, err := m.policy.users.GetByID(r.Context(), claims.UserID)
		if err != nil || user == nil || !user.Enabled {
			return nativeAccessRefusal("forbidden")
		}
		subject := access.GroupSubject{AccountID: user.ID, ProfileID: profile}
		if m.policy.groups != nil {
			subject, err = access.GroupSubjectFromContext(r.Context(), user.ID, profile)
			if err != nil {
				return nativeAccessRefusal("forbidden")
			}
		}
		effective, err := access.EffectivePolicyForSubject(r.Context(), user, subject, m.policy.groups)
		if err != nil {
			return nativeAccessRefusal("forbidden")
		}
		// The route already established the permission-only gate. This is its
		// existing complete-target decision, refreshed for this selected phase.
		decision, err := m.policy.checkPermission(r, policy.PermissionInput{UserID: user.ID, Role: user.Role, UserEnabled: user.Enabled,
			AssignedPermissions: slices.Clone(effective.Permissions), Permission: policy.PermissionMetadataCuration,
			DeclaredProfileID: profile, ActingAsPrimary: primary, TargetLibraryIDs: slices.Clone(libraries),
			UserLibraryIDs: slices.Clone(effective.LibraryIDs), UserLibrariesRestricted: effective.LibraryIDs != nil})
		if err != nil {
			return nativeAccessRefusal("native_storage_unavailable")
		}
		if !decision.Allowed {
			return nativeAccessRefusal("forbidden")
		}
		return nil
	}
	primary, err := actingAdminAllowed(r, claims.UserID, m.primary)
	if err != nil {
		return nativeAccessRefusal("native_storage_unavailable")
	}
	if claims.Role == "admin" && primary {
		return nil
	}
	if gate == "admin" || m.legacy == nil {
		return nativeAccessRefusal("forbidden")
	}
	user, err := m.legacy.users.GetByID(r.Context(), claims.UserID)
	if err != nil || user == nil || !user.Enabled {
		return nativeAccessRefusal("forbidden")
	}
	permitted := auth.HasEffectivePermission(user, auth.PermissionMetadataCuration)
	if claims.Role == "admin" {
		permitted = auth.HasAssignedPermission(user, auth.PermissionMetadataCuration)
	}
	if !permitted {
		return nativeAccessRefusal("forbidden")
	}
	effective, err := access.EffectivePolicyForUser(r.Context(), user, m.legacy.groups)
	if err != nil || !metadataTargetWithinUserLibraries(effective.LibraryIDs, libraries) {
		return nativeAccessRefusal("forbidden")
	}
	return nil
}

// NativeMutationRequestSnapshot strips body and mutable header maps. It is
// captured before v2 gates and consumed only after those gates resolve scope.
func NativeMutationRequestSnapshot(r *http.Request) *http.Request {
	copy := r.Clone(context.Background())
	copy.Body = nil
	copy.Header = make(http.Header)
	for _, key := range []string{"Authorization", "X-Profile-Id", "X-Profile-Token", siloDeviceIDHeader} {
		copy.Header.Set(key, strings.Clone(r.Header.Get(key)))
	}
	return copy
}

func (o *NativeMutationOrigin) DeviceID() string {
	if o == nil || o.request == nil {
		return ""
	}
	return o.request.Header.Get(siloDeviceIDHeader)
}
