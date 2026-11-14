package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/ratelimit"
	"github.com/Silo-Server/silo-server/internal/sessioninvalidation"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// Profile editing for an organization administrator. Adding and removing a
// profile run through Silo's profile lifecycle (canonical settings, personal
// collections, avatar and download cleanup); rename and clearing a forgotten
// PIN are single statements in adminpeople. An administrator never sets a PIN
// and never edits parental fields.

var errSupportProfileLimit = errors.New("profile limit reached")

func (h *BloemOrganizationSupportHandler) profileLifecycle() (*ProfileHandler, bool) {
	if h.admin == nil || h.admin.storeProv == nil || h.admin.userRepo == nil {
		return nil, false
	}
	return h.admin.adminResourceProfileHandler(), true
}

func supportProfileID(w http.ResponseWriter, r *http.Request) (string, bool) {
	profileID := strings.TrimSpace(chi.URLParam(r, "profile_id"))
	if profileID == "" {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return "", false
	}
	return profileID, true
}

// writeProfileError adds the profile-only answers to writeSupportError.
func (h *BloemOrganizationSupportHandler) writeProfileError(w http.ResponseWriter, r *http.Request, err error, organizationID uuid.UUID, accountID int) {
	var revision *adminpeople.ProfileRevisionError
	switch {
	case errors.As(err, &revision):
		writeJSON(w, http.StatusConflict, struct {
			Error           string `json:"error"`
			Message         string `json:"message"`
			CurrentRevision int64  `json:"current_revision"`
		}{supportStateChangedCode, "The profile changed; reload and retry", revision.Current})
	case errors.Is(err, adminpeople.ErrProfileNameConflict):
		writeError(w, http.StatusConflict, "name_conflict", "A profile with this name already exists")
	case errors.Is(err, adminpeople.ErrPrimaryProfileProtected):
		writeError(w, http.StatusConflict, "primary_profile_protected", "The primary profile cannot be removed")
	case errors.Is(err, errSupportProfileLimit):
		writeError(w, http.StatusUnprocessableEntity, "profile_limit_reached", "This account has reached its profile limit")
	case errors.Is(err, adminpeople.ErrInvalidProfileName):
		writeAdminValidation(w, map[string]string{"name": "must be 1 to 100 characters without control characters"})
	default:
		h.writeSupportError(w, r, err, organizationID, accountID)
	}
}

type supportProfileBody struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Name             string `json:"name"`
}

// HandleCreateProfile handles POST /admin/organization/people/{account_id}/profiles.
func (h *BloemOrganizationSupportHandler) HandleCreateProfile(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	accountID, ok := supportAccountID(w, r)
	if !ok {
		return
	}
	name, groupID, ok := h.parseCreateProfile(w, r, request.Tenant.OrganizationID, accountID)
	if !ok {
		return
	}
	organizationID := request.Tenant.OrganizationID
	lifecycle, ok := h.profileLifecycle()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return
	}
	ctx := withAdminResourceOrganization(r.Context(), organizationID)
	user, err := h.admin.userRepo.GetByID(ctx, accountID)
	if err != nil || user == nil {
		// Same answer as an account outside the organization.
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	limit, inheritedGroupID, err := lifecycle.effectiveProfileLimit(ctx, user)
	if err != nil {
		h.writeSupportError(w, r, err, organizationID, accountID)
		return
	}
	store, err := h.admin.storeProv.ForUser(ctx, accountID)
	if err != nil {
		h.writeSupportError(w, r, err, organizationID, accountID)
		return
	}
	transactioner, ok := store.(userstore.PreferenceSettingsTransactioner)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return
	}

	profile := userstore.Profile{
		ID: uuid.NewString(), Name: name, OrganizationID: organizationID.String(),
		AccessGroupID: inheritedGroupID, ShowForcedSubtitles: true,
	}
	if groupID != nil {
		group := int64(*groupID)
		profile.AccessGroupID = &group
	}
	var refusal error
	var changedKeys []string
	for attempt := 1; ; attempt++ {
		refusal, changedKeys, err = h.createProfileOnce(ctx, transactioner, request, accountID, profile, limit)
		if err != nil && attempt < 3 && isDeadlock(err) {
			continue
		}
		break
	}
	if err == nil && refusal != nil {
		err = refusal
	}
	if err != nil {
		if isProfileEntitlementLimitError(err) {
			err = errSupportProfileLimit
		}
		h.writeProfileError(w, r, err, organizationID, accountID)
		return
	}
	for _, key := range changedKeys {
		publishUserSettingsEvent(ctx, lifecycle.EventsHub, accountID, profile.ID, key, string(settingscontract.ScopeProfile))
	}
	person, err := h.people.Get(r.Context(), organizationID, accountID)
	if err != nil {
		h.writeSupportError(w, r, err, organizationID, accountID)
		return
	}
	for _, created := range person.Profiles {
		if created.ID == profile.ID {
			writeJSON(w, http.StatusCreated, struct {
				Profile adminpeople.ProfileSummary `json:"profile"`
				Person  adminpeople.PersonSummary  `json:"person"`
			}{created, person})
			return
		}
	}
	writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
}

// createProfileOnce runs one create in Silo's preference-settings transaction,
// so the authority judgement, the profile, its inherited settings and the audit
// event commit together. A refusal commits only its audit row.
func (h *BloemOrganizationSupportHandler) createProfileOnce(ctx context.Context, transactioner userstore.PreferenceSettingsTransactioner, request supportRequest, accountID int, profile userstore.Profile, limit int) (refusal error, changedKeys []string, err error) {
	organizationID := request.Tenant.OrganizationID
	err = transactioner.WithPreferenceSettingsTransaction(ctx, func(w userstore.PreferenceSettingsWriter) error {
		audited, ok := w.(userstore.AuditedProfileWriter)
		if !ok || audited.AuditTx() == nil {
			return errors.New("user store has no transaction for support audit")
		}
		tx := audited.AuditTx()
		if err := adminpeople.TakeBudget(ctx, tx, adminpeople.BudgetMutate, organizationID, request.Actor.AccountID, itoa(accountID)); err != nil {
			return err
		}
		locked, err := adminpeople.LockSupportTarget(ctx, tx, organizationID, request.Actor, accountID, "people.profile_create_refused", "profile", profile.ID)
		if err != nil {
			return captureRefusal(err, &refusal)
		}
		event := adminpeople.SupportEvent(request.Actor, organizationID, accountID, "people.profile_create_refused", "profile", profile.ID, locked)
		var conflict bool
		var count int
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_profiles WHERE user_id = $1 AND lower(btrim(name)) = lower($2)), (SELECT count(*) FROM user_profiles WHERE user_id = $1)`, accountID, profile.Name).Scan(&conflict, &count); err != nil {
			return err
		}
		if conflict {
			return captureRefusal(adminpeople.RefuseAudited(ctx, tx, event, "name_conflict", adminpeople.ErrProfileNameConflict), &refusal)
		}
		if limit >= 1 && count >= limit {
			return captureRefusal(adminpeople.RefuseAudited(ctx, tx, event, "profile_limit_reached", errSupportProfileLimit), &refusal)
		}
		if err := w.CreateProfile(ctx, profile); err != nil {
			return err
		}
		inherited, err := planInheritedLegacyUserSettings(ctx, w)
		if err != nil {
			return err
		}
		changedKeys, err = writeCanonicalSettingsSync(ctx, w, userstore.SettingIdentity{Scope: settingscontract.ScopeProfile, ProfileID: profile.ID}, inherited)
		if err != nil {
			return err
		}
		event.Action = "people.profile_created"
		event.Outcome = "success"
		event.After = map[string]any{"name": profile.Name, "group_id": profile.AccessGroupID}
		return adminpeople.RecordAudit(ctx, tx, event)
	})
	return refusal, changedKeys, err
}

// captureRefusal turns an audited refusal into a nil error (so the surrounding
// transaction commits its audit row) and hands the cause to the caller.
func captureRefusal(err error, out *error) error {
	var refusal *adminpeople.RefusalError
	if errors.As(err, &refusal) {
		*out = refusal.Unwrap()
		return nil
	}
	return err
}

// HandleRenameProfile handles PATCH /admin/organization/people/{account_id}/profiles/{profile_id}/details.
func (h *BloemOrganizationSupportHandler) HandleRenameProfile(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	accountID, ok := supportAccountID(w, r)
	if !ok {
		return
	}
	profileID, ok := supportProfileID(w, r)
	if !ok {
		return
	}
	var body supportProfileBody
	if !decodeAdminPlatformJSON(w, r, &body) {
		return
	}
	fields := map[string]string{}
	if body.ExpectedRevision <= 0 {
		fields["expected_revision"] = "must be the profile's current revision"
	}
	if _, err := adminpeople.ValidateProfileName(body.Name); err != nil {
		fields["name"] = "must be 1 to 100 characters without control characters"
	}
	if len(fields) > 0 {
		writeAdminValidation(w, fields)
		return
	}
	result, err := h.people.RenameProfile(r.Context(), request.Tenant.OrganizationID, request.Actor, accountID, profileID, body.Name, body.ExpectedRevision)
	if err != nil {
		h.writeProfileError(w, r, err, request.Tenant.OrganizationID, accountID)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Profile adminpeople.ProfileSummary `json:"profile"`
		Person  adminpeople.PersonSummary  `json:"person"`
	}{result.Profile, result.Person})
}

// HandleClearProfilePIN handles DELETE /admin/organization/people/{account_id}/profiles/{profile_id}/pin.
func (h *BloemOrganizationSupportHandler) HandleClearProfilePIN(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	accountID, ok := supportAccountID(w, r)
	if !ok {
		return
	}
	profileID, ok := supportProfileID(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &body) {
		return
	}
	if body.ExpectedRevision <= 0 {
		writeAdminValidation(w, map[string]string{"expected_revision": "must be the profile's current revision"})
		return
	}
	result, err := h.people.ClearProfilePIN(r.Context(), request.Tenant.OrganizationID, request.Actor, accountID, profileID, body.ExpectedRevision)
	if err != nil {
		h.writeProfileError(w, r, err, request.Tenant.OrganizationID, accountID)
		return
	}
	// A cleared PIN starts with a clean attempt count, so a profile locked by
	// wrong guesses works at once. The PIN is already committed, so the reset
	// must not depend on the client staying connected.
	if lifecycle, ok := h.profileLifecycle(); ok && lifecycle.PINAttempts != nil {
		lifecycle.PINAttempts.Reset(context.WithoutCancel(r.Context()), ratelimit.ProfilePINKey(accountID, profileID))
	}
	writeJSON(w, http.StatusOK, struct {
		Profile adminpeople.ProfileSummary `json:"profile"`
	}{result.Profile})
}

// HandleRemoveProfile handles DELETE /admin/organization/people/{account_id}/profiles/{profile_id}.
func (h *BloemOrganizationSupportHandler) HandleRemoveProfile(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	accountID, ok := supportAccountID(w, r)
	if !ok {
		return
	}
	profileID, ok := supportProfileID(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &body) {
		return
	}
	if body.ExpectedRevision <= 0 {
		writeAdminValidation(w, map[string]string{"expected_revision": "must be the profile's current revision"})
		return
	}
	organizationID := request.Tenant.OrganizationID
	lifecycle, ok := h.profileLifecycle()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return
	}
	plan, err := h.people.PlanProfileRemoval(r.Context(), organizationID, request.Actor, accountID, profileID, body.ExpectedRevision)
	if err != nil {
		h.writeProfileError(w, r, err, organizationID, accountID)
		return
	}
	ctx := withAdminResourceOrganization(r.Context(), organizationID)
	store, err := h.admin.storeProv.ForUser(ctx, accountID)
	if err != nil {
		h.writeSupportError(w, r, err, organizationID, accountID)
		return
	}
	deleter, ok := store.(userstore.AuditedProfileDeleter)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return
	}
	var deleted *userstore.Profile
	var collections []string
	for attempt := 1; ; attempt++ {
		collections, err = deleter.DeleteProfileAudited(ctx, profileID, func(tx pgx.Tx, found *userstore.Profile) error {
			deleted = found
			return adminpeople.FinishProfileRemoval(ctx, tx, organizationID, request.Actor, accountID, plan, found.Name)
		})
		if err != nil && attempt < 3 && isDeadlock(err) {
			continue
		}
		break
	}
	if err != nil {
		// The delete rolled back. A refusal found under the delete's locks is
		// audited here, in its own transaction.
		var protected *adminpeople.TargetProtectedError
		if errors.As(err, &protected) {
			h.auditRemovalRefusal(ctx, request, accountID, profileID, plan, string(protected.Reason), err)
		}
		h.writeProfileError(w, r, err, organizationID, accountID)
		return
	}
	if deleted != nil {
		lifecycle.completeProfileDeleteAfterCommit(ctx, accountID, deleted)
	}
	removeDeletedCollectionArtwork(ctx, lifecycle.ArtworkStore, userCollectionImagePrefix, collections...)
	if h.profileSessionsRevoked != nil {
		if err := sessioninvalidation.Run(ctx, func(callbackCtx context.Context) error {
			return h.profileSessionsRevoked(callbackCtx, accountID, []string{profileID})
		}); err != nil {
			supportDependencyUnavailable(w)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *BloemOrganizationSupportHandler) auditRemovalRefusal(ctx context.Context, request supportRequest, accountID int, profileID string, plan adminpeople.ProfileRemovalPlan, reason string, cause error) {
	event := adminpeople.SupportEvent(request.Actor, request.Tenant.OrganizationID, accountID, "people.profile_remove_refused", "profile", profileID, plan.Authority)
	_, _ = adminpeople.RunSupportTx(context.WithoutCancel(ctx), h.pool.Begin, func(tx pgx.Tx) (struct{}, error) {
		return struct{}{}, adminpeople.RefuseAudited(ctx, tx, event, reason, cause)
	})
}

// parseCreateProfile reads and validates a profile create request: the name,
// and the access group when one is named, which must be this organization's.
func (h *BloemOrganizationSupportHandler) parseCreateProfile(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, accountID int) (string, *int, bool) {
	var body struct {
		Name    string `json:"name"`
		GroupID *int   `json:"group_id"`
	}
	if !decodeAdminPlatformJSON(w, r, &body) {
		return "", nil, false
	}
	name, nameErr := adminpeople.ValidateProfileName(body.Name)
	fields := map[string]string{}
	if nameErr != nil {
		fields["name"] = "must be 1 to 100 characters without control characters"
	}
	if body.GroupID != nil && *body.GroupID <= 0 {
		fields["group_id"] = "must be a group of this organization"
	}
	if len(fields) > 0 {
		writeAdminValidation(w, fields)
		return "", nil, false
	}
	if body.GroupID != nil {
		var exists bool
		if err := h.pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM access_groups WHERE organization_id = $1 AND id = $2)`, organizationID, *body.GroupID).Scan(&exists); err != nil {
			h.writeSupportError(w, r, err, organizationID, accountID)
			return "", nil, false
		}
		if !exists {
			writeAdminValidation(w, map[string]string{"group_id": "must be a group of this organization"})
			return "", nil, false
		}
	}
	return name, body.GroupID, true
}
