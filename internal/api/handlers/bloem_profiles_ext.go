package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// SetLifecycleIdempotency installs durable coordination for direct household
// profile creates, updates, and deletes.
func (h *ProfileHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, digester lifecycleidempotency.RequestDigester) {
	h.lifecycle = coordinator
	h.digest = digester
}

// HandleGetProfile handles GET /profiles/{id}.
//
// The household list at GET /profiles is account-scoped and stays that way. A
// direct-profile session is bound to one profile and needs to read that
// profile's own record — its name, avatar, and preferences — which the login
// response does not carry; RequireOwnDirectProfile holds such a session to its
// own id, and an account session may read any profile it owns.
func (h *ProfileHandler) HandleGetProfile(w http.ResponseWriter, r *http.Request) {
	userID := apimw.GetUserID(r.Context())
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	profileID := chi.URLParam(r, "id")
	if profileID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Profile ID is required")
		return
	}

	store, err := h.storeProvider.ForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return
	}
	profile, err := store.GetProfile(r.Context(), profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load profile")
		return
	}
	if profile == nil {
		writeError(w, http.StatusNotFound, "not_found", "Profile not found")
		return
	}

	writeJSON(w, http.StatusOK, h.toProfileResponse(r.Context(), store, *profile))
}

func isProfileEntitlementLimitError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == "user_profiles_entitlement_limit"
}

func (h *ProfileHandler) effectiveProfileLimit(ctx context.Context, user *models.User) (int, *int64, error) {
	if user == nil {
		return 0, nil, nil
	}
	limit := user.MaxProfiles
	if h.AccessGroups == nil {
		return limit, cloneGroupID(user.AccessGroupID), nil
	}
	explicitOrganizationID := adminResourceOrganization(ctx)
	organizationID := explicitOrganizationID
	if organizationID == uuid.Nil {
		if tenant, ok := tenancy.FromContext(ctx); ok {
			organizationID = tenant.OrganizationID
		}
	}
	if organizationID == uuid.Nil {
		if user.AccessGroupID == nil {
			return limit, nil, nil
		}
		group, err := h.AccessGroups.GetForAccount(ctx, user.ID, *user.AccessGroupID)
		if err != nil {
			return 0, nil, err
		}
		return strictestProfileLimit(limit, group.MaxProfiles, group.ManagedTemplateKey != nil), cloneGroupID(&group.ID), nil
	}
	var group *access.Group
	var err error
	if explicitOrganizationID == uuid.Nil && user.AccessGroupID != nil {
		group, err = h.AccessGroups.Get(ctx, organizationID, *user.AccessGroupID)
		if errors.Is(err, access.ErrGroupNotFound) {
			group, err = h.AccessGroups.GetDefault(ctx, organizationID)
		}
	} else {
		group, err = h.AccessGroups.GetDefault(ctx, organizationID)
	}
	if err != nil {
		return 0, nil, err
	}
	return strictestProfileLimit(limit, group.MaxProfiles, group.ManagedTemplateKey != nil), cloneGroupID(&group.ID), nil
}

func cloneGroupID(id *int64) *int64 {
	if id == nil {
		return nil
	}
	copy := *id
	return &copy
}

func strictestProfileLimit(accountLimit, groupLimit int, managed bool) int {
	// Managed template max_profiles=0 means no secondary profiles; the primary
	// profile provisioned with an account still occupies the single allowed row.
	// Legacy unmanaged groups retain their historical 0=unlimited semantics.
	if managed && groupLimit == 0 {
		groupLimit = 1
	}
	if groupLimit <= 0 || accountLimit > 0 && accountLimit <= groupLimit {
		return accountLimit
	}
	return groupLimit
}

func profilesForOrganization(ctx context.Context, profiles []userstore.Profile) []userstore.Profile {
	organizationID := adminResourceOrganization(ctx)
	if organizationID == uuid.Nil {
		if tenant, ok := tenancy.FromContext(ctx); ok {
			organizationID = tenant.OrganizationID
		}
	}
	if organizationID == uuid.Nil {
		return profiles
	}
	filtered := make([]userstore.Profile, 0, len(profiles))
	for _, profile := range profiles {
		// Empty is the legacy/pre-migration representation of the deployment
		// default organization and must remain fail-closed for cap accounting.
		if profile.OrganizationID == "" || profile.OrganizationID == organizationID.String() {
			filtered = append(filtered, profile)
		}
	}
	return filtered
}

type createProfileRequest = ProfileCreateRequest

type updateProfileRequest = ProfileUpdateRequest

type profileResponse = ProfileView
