package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const entitlementConfirmationTTL = 10 * time.Minute

var errEntitlementConfirmationStale = errors.New("entitlement confirmation is stale")

type entitlementReceipt struct {
	TemplateKey      string
	TemplateRevision int64
	Result           entitlements.ApplyResult
}

// EntitlementTemplateHandlerStore is the complete platform API boundary. The
// concrete entitlements.Store keeps revision and materialization transactions
// below HTTP concerns.
type EntitlementTemplateHandlerStore interface {
	List(context.Context, bool) ([]entitlements.Template, error)
	Get(context.Context, string, int64) (entitlements.Template, error)
	Latest(context.Context, string) (entitlements.Template, error)
	ListRevisions(context.Context, string) ([]entitlements.Template, error)
	Create(context.Context, entitlements.CreateTemplateInput) (entitlements.Template, error)
	Revise(context.Context, string, int64, entitlements.ReviseTemplateInput) (entitlements.Template, error)
	Clone(context.Context, string, int64, entitlements.CreateTemplateInput) (entitlements.Template, error)
	Archive(context.Context, string, int64) (entitlements.Template, error)
	ApplyTemplate(context.Context, uuid.UUID, string, int64, bool) (entitlements.ApplyResult, error)
	ApplyDefaultAccountTemplate(context.Context, int, string, int64, bool) (entitlements.ApplyResult, error)
	GetOrganizationEntitlement(context.Context, uuid.UUID) (entitlements.OrganizationEntitlement, error)
	GetDefaultAccountEntitlement(context.Context, int) (entitlements.AccountEntitlement, error)
}

type entitlementAuditStore interface {
	RecordAudit(context.Context, entitlements.AuditEvent) error
	ListOrganizationAudit(context.Context, uuid.UUID) ([]entitlements.AuditEvent, error)
}

type entitlementReceiptStore interface {
	LoadApplyReceipt(context.Context, int, string, string, string) (entitlements.ApplyReceipt, bool, error)
	SaveApplyReceipt(context.Context, int, string, string, string, string, int64, entitlements.ApplyResult) (bool, error)
}

type entitlementAtomicApplyStore interface {
	ApplyTemplateWithReceipt(context.Context, int, uuid.UUID, string, string, int64, string) (entitlements.ApplyResult, bool, error)
	ApplyDefaultAccountTemplateWithReceipt(context.Context, int, int, string, string, int64, string) (entitlements.ApplyResult, bool, error)
}

type EntitlementTemplatesHandler struct {
	store  EntitlementTemplateHandlerStore
	secret []byte
	now    func() time.Time
	mu     sync.Mutex
	// Applying a materialization is itself idempotent; this bounded process map
	// also gives retries the identical first response. A restart may forget the
	// response but cannot duplicate policy effects.
	receipts map[string]entitlementReceipt
}

func NewEntitlementTemplatesHandler(store EntitlementTemplateHandlerStore, secret []byte) *EntitlementTemplatesHandler {
	return &EntitlementTemplatesHandler{
		store: store, secret: append([]byte(nil), secret...), now: time.Now,
		receipts: make(map[string]entitlementReceipt),
	}
}

type entitlementPolicyJSON struct {
	AllLibraries             bool     `json:"all_libraries"`
	LibraryIDs               []int    `json:"library_ids"`
	PlaybackAllowed          bool     `json:"playback_allowed"`
	MaxStreams               int      `json:"max_streams"`
	MaxProfiles              int      `json:"max_profiles"`
	TranscodeAllowed         bool     `json:"transcode_allowed"`
	MaxTranscodes            int      `json:"max_transcodes"`
	DownloadAllowed          bool     `json:"download_allowed"`
	DownloadTranscodeAllowed bool     `json:"download_transcode_allowed"`
	MaxPlaybackQuality       string   `json:"max_playback_quality"`
	AllowedPermissions       []string `json:"allowed_permissions"`
	RequestsAllowed          bool     `json:"requests_allowed"`
}

func policyToJSON(policy entitlements.Policy) entitlementPolicyJSON {
	return entitlementPolicyJSON{
		AllLibraries: policy.LibraryIDs == nil, LibraryIDs: policy.LibraryIDs,
		PlaybackAllowed: policy.PlaybackAllowed, MaxStreams: policy.MaxStreams,
		MaxProfiles: policy.MaxProfiles, TranscodeAllowed: policy.TranscodeAllowed,
		MaxTranscodes: policy.MaxTranscodes, DownloadAllowed: policy.DownloadAllowed,
		DownloadTranscodeAllowed: policy.DownloadTranscodeAllowed,
		MaxPlaybackQuality:       policy.MaxPlaybackQuality,
		AllowedPermissions:       policy.AllowedPermissions, RequestsAllowed: policy.RequestsAllowed,
	}
}

func (p entitlementPolicyJSON) policy() entitlements.Policy {
	libraries := p.LibraryIDs
	if p.AllLibraries || libraries == nil {
		libraries = nil
	}
	return entitlements.Policy{
		LibraryIDs: libraries, PlaybackAllowed: p.PlaybackAllowed, MaxStreams: p.MaxStreams,
		MaxProfiles: p.MaxProfiles, TranscodeAllowed: p.TranscodeAllowed,
		MaxTranscodes: p.MaxTranscodes, DownloadAllowed: p.DownloadAllowed,
		DownloadTranscodeAllowed: p.DownloadTranscodeAllowed,
		MaxPlaybackQuality:       p.MaxPlaybackQuality,
		AllowedPermissions:       p.AllowedPermissions, RequestsAllowed: p.RequestsAllowed,
	}
}

type entitlementTemplateJSON struct {
	Key       string                `json:"key"`
	Name      string                `json:"name"`
	Revision  int64                 `json:"revision"`
	Enabled   bool                  `json:"enabled"`
	Archived  bool                  `json:"archived"`
	Status    string                `json:"status"`
	Policy    entitlementPolicyJSON `json:"policy"`
	CreatedAt time.Time             `json:"created_at"`
}

func templateToJSON(item entitlements.Template) entitlementTemplateJSON {
	status := "disabled"
	if item.Archived {
		status = "archived"
	} else if item.Enabled {
		status = "enabled"
	}
	return entitlementTemplateJSON{
		Key: item.Key, Name: item.Name, Revision: item.Revision, Enabled: item.Enabled,
		Archived: item.Archived, Status: status, Policy: policyToJSON(item.Policy), CreatedAt: item.CreatedAt,
	}
}

func (h *EntitlementTemplatesHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	if !h.requirePlatform(w, r) {
		return
	}
	includeArchived := true
	if raw := strings.TrimSpace(r.URL.Query().Get("include_archived")); raw != "" {
		includeArchived, _ = strconv.ParseBool(raw)
	}
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("status")), "enabled") {
		includeArchived = false
	}
	items, err := h.store.List(r.Context(), includeArchived)
	if err != nil {
		h.writeError(w, err)
		return
	}
	response := make([]entitlementTemplateJSON, 0, len(items))
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("status")), "enabled") && !item.Enabled {
			continue
		}
		response = append(response, templateToJSON(item))
	}
	writeJSON(w, http.StatusOK, struct {
		Templates []entitlementTemplateJSON `json:"templates"`
	}{response})
}

func (h *EntitlementTemplatesHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	var request struct {
		Key     string                `json:"key"`
		Name    string                `json:"name"`
		Enabled bool                  `json:"enabled"`
		Policy  entitlementPolicyJSON `json:"policy"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	item, err := h.store.Create(r.Context(), entitlements.CreateTemplateInput{Key: request.Key, Name: request.Name, Enabled: request.Enabled, Policy: request.Policy.policy()})
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.audit(r, claims, "entitlement_template.created", item.Key, item.Revision, uuid.Nil)
	writeTemplateJSON(w, http.StatusCreated, item)
}

func (h *EntitlementTemplatesHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	if !h.requirePlatform(w, r) {
		return
	}
	key := chi.URLParam(r, "key")
	var item entitlements.Template
	var err error
	if raw := strings.TrimSpace(r.URL.Query().Get("revision")); raw != "" {
		revision, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || revision <= 0 {
			writeAdminValidation(w, map[string]string{"revision": "must be positive"})
			return
		}
		item, err = h.store.Get(r.Context(), key, revision)
	} else {
		item, err = h.store.Latest(r.Context(), key)
	}
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeTemplateJSON(w, http.StatusOK, item)
}

func (h *EntitlementTemplatesHandler) HandleListRevisions(w http.ResponseWriter, r *http.Request) {
	if !h.requirePlatform(w, r) {
		return
	}
	items, err := h.store.ListRevisions(r.Context(), chi.URLParam(r, "key"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	response := make([]entitlementTemplateJSON, 0, len(items))
	for _, item := range items {
		response = append(response, templateToJSON(item))
	}
	writeJSON(w, http.StatusOK, struct {
		Revisions []entitlementTemplateJSON `json:"revisions"`
	}{response})
}

func (h *EntitlementTemplatesHandler) HandleRevise(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64                  `json:"expected_revision"`
		SourceRevision   int64                  `json:"source_revision,omitempty"`
		Name             string                 `json:"name"`
		Enabled          bool                   `json:"enabled"`
		Policy           *entitlementPolicyJSON `json:"policy,omitempty"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	var policy entitlements.Policy
	if request.SourceRevision > 0 {
		source, err := h.store.Get(r.Context(), chi.URLParam(r, "key"), request.SourceRevision)
		if err != nil {
			h.writeError(w, err)
			return
		}
		policy = source.Policy
	} else if request.Policy != nil {
		policy = request.Policy.policy()
	} else {
		writeAdminValidation(w, map[string]string{"policy": "is required unless source_revision selects rollback history"})
		return
	}
	item, err := h.store.Revise(r.Context(), chi.URLParam(r, "key"), request.ExpectedRevision, entitlements.ReviseTemplateInput{Name: request.Name, Enabled: request.Enabled, Policy: policy})
	if err != nil {
		h.writeError(w, err)
		return
	}
	action := "entitlement_template.revised"
	if request.SourceRevision > 0 {
		action = "entitlement_template.rollback_revision_created"
	}
	h.audit(r, claims, action, item.Key, item.Revision, uuid.Nil)
	writeTemplateJSON(w, http.StatusCreated, item)
}

func (h *EntitlementTemplatesHandler) HandleClone(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	var request struct {
		SourceRevision int64  `json:"source_revision"`
		Key            string `json:"key"`
		Name           string `json:"name"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	item, err := h.store.Clone(r.Context(), chi.URLParam(r, "key"), request.SourceRevision, entitlements.CreateTemplateInput{Key: request.Key, Name: request.Name, Enabled: false})
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.audit(r, claims, "entitlement_template.cloned", item.Key, item.Revision, uuid.Nil)
	writeTemplateJSON(w, http.StatusCreated, item)
}

func (h *EntitlementTemplatesHandler) HandleArchive(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	item, err := h.store.Archive(r.Context(), chi.URLParam(r, "key"), request.ExpectedRevision)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.audit(r, claims, "entitlement_template.archived", item.Key, item.Revision, uuid.Nil)
	writeTemplateJSON(w, http.StatusOK, item)
}

func writeTemplateJSON(w http.ResponseWriter, status int, item entitlements.Template) {
	writeJSON(w, status, struct {
		Template entitlementTemplateJSON `json:"template"`
	}{Template: templateToJSON(item)})
}

func (h *EntitlementTemplatesHandler) HandleGetOrganizationEntitlement(w http.ResponseWriter, r *http.Request) {
	if !h.requirePlatform(w, r) {
		return
	}
	organizationID, ok := adminPlatformPathUUID(w, r, "id")
	if !ok {
		return
	}
	item, err := h.store.GetOrganizationEntitlement(r.Context(), organizationID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	type managedGroup struct {
		ID     int64                 `json:"id"`
		Name   string                `json:"name"`
		Policy entitlementPolicyJSON `json:"policy"`
	}
	var group *managedGroup
	if item.GroupID > 0 {
		group = &managedGroup{ID: item.GroupID, Name: item.GroupName, Policy: policyToJSON(item.Policy)}
	}
	writeJSON(w, http.StatusOK, struct {
		TemplateKey         string        `json:"template_key"`
		TemplateRevision    int64         `json:"template_revision"`
		ManagedDefaultGroup *managedGroup `json:"managed_default_group"`
		TenantLimits        struct {
			Slots      int `json:"slots"`
			Transcodes int `json:"transcodes"`
		} `json:"tenant_limits"`
		LibraryIDs       []int      `json:"library_ids"`
		LastReconciledAt *time.Time `json:"last_reconciled_at"`
		AuditHistoryHref string     `json:"audit_history_href"`
	}{TemplateKey: item.TemplateKey, TemplateRevision: item.TemplateRevision, ManagedDefaultGroup: group,
		TenantLimits: struct {
			Slots      int `json:"slots"`
			Transcodes int `json:"transcodes"`
		}{item.Slots, item.Transcodes},
		LibraryIDs: item.Policy.LibraryIDs, LastReconciledAt: item.LastReconciledAt,
		AuditHistoryHref: "/api/v2/admin/platform/organizations/" + organizationID.String() + "/entitlement/audit"})
}

func (h *EntitlementTemplatesHandler) HandleOrganizationAudit(w http.ResponseWriter, r *http.Request) {
	if !h.requirePlatform(w, r) {
		return
	}
	organizationID, ok := adminPlatformPathUUID(w, r, "id")
	if !ok {
		return
	}
	durable, ok := h.store.(entitlementAuditStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", "Entitlement audit history is unavailable")
		return
	}
	events, err := durable.ListOrganizationAudit(r.Context(), organizationID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Events []entitlements.AuditEvent `json:"events"`
	}{Events: events})
}

type entitlementApplyRequest struct {
	TemplateKey       string `json:"template_key"`
	TemplateRevision  int64  `json:"template_revision"`
	ConfirmationToken string `json:"confirmation_token,omitempty"`
	DryRunToken       string `json:"dry_run_token,omitempty"`
	IdempotencyKey    string `json:"idempotency_key,omitempty"`
}

func (r *entitlementApplyRequest) resolveTokenAlias() {
	if r.ConfirmationToken == "" {
		r.ConfirmationToken = r.DryRunToken
	}
}

func entitlementAccountPathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if chi.URLParam(r, "account_id") != "" {
		return v2PositivePathID(w, r, "account_id")
	}
	return v2PositivePathID(w, r, "user_id")
}

func (h *EntitlementTemplatesHandler) HandleOrganizationDryRun(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	organizationID, ok := adminPlatformPathUUID(w, r, "id")
	if !ok {
		return
	}
	var request entitlementApplyRequest
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if !validEntitlementSelection(request) {
		writeAdminValidation(w, map[string]string{"template": "must include key and positive revision"})
		return
	}
	result, err := h.store.ApplyTemplate(r.Context(), organizationID, request.TemplateKey, request.TemplateRevision, true)
	if err != nil {
		h.writeError(w, err)
		return
	}
	expiresAt := h.now().UTC().Add(entitlementConfirmationTTL)
	token, err := h.signConfirmation(entitlementConfirmationClaims{OrganizationID: organizationID, TemplateKey: request.TemplateKey, TemplateRevision: request.TemplateRevision, AccountID: claims.AccountID, PreviewHash: entitlementPreviewHash(result), ExpiresAt: expiresAt.Unix()})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", "Entitlement confirmation is unavailable")
		return
	}
	h.writeDryRun(w, result, token, expiresAt)
}

func (h *EntitlementTemplatesHandler) HandleOrganizationApply(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	organizationID, ok := adminPlatformPathUUID(w, r, "id")
	if !ok {
		return
	}
	var request entitlementApplyRequest
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	request.resolveTokenAlias()
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if !validEntitlementSelection(request) || request.ConfirmationToken == "" || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 200 {
		writeAdminValidation(w, map[string]string{"request": "requires template key/revision, confirmation_token, and idempotency_key"})
		return
	}
	confirmation, err := h.parseConfirmation(request.ConfirmationToken)
	if err != nil || confirmation.OrganizationID != organizationID || confirmation.TemplateKey != request.TemplateKey || confirmation.TemplateRevision != request.TemplateRevision || confirmation.AccountID != claims.AccountID {
		writeAdminValidation(w, map[string]string{"confirmation_token": "is invalid, expired, or belongs to another apply target"})
		return
	}
	var result entitlements.ApplyResult
	var repeated bool
	if atomic, ok := h.store.(entitlementAtomicApplyStore); ok {
		result, repeated, err = atomic.ApplyTemplateWithReceipt(r.Context(), claims.AccountID, organizationID, request.IdempotencyKey, request.TemplateKey, request.TemplateRevision, confirmation.PreviewHash)
	} else {
		receiptKey := strconv.Itoa(claims.AccountID) + ":" + organizationID.String() + ":" + request.IdempotencyKey
		result, repeated, err = h.applyOnce(r.Context(), claims.AccountID, "organization", organizationID.String(), request.IdempotencyKey, receiptKey, request.TemplateKey, request.TemplateRevision, func() (entitlements.ApplyResult, error) {
			preview, previewErr := h.store.ApplyTemplate(r.Context(), organizationID, request.TemplateKey, request.TemplateRevision, true)
			if previewErr != nil {
				return entitlements.ApplyResult{}, previewErr
			}
			if entitlementPreviewHash(preview) != confirmation.PreviewHash {
				return entitlements.ApplyResult{}, errEntitlementConfirmationStale
			}
			return h.store.ApplyTemplate(r.Context(), organizationID, request.TemplateKey, request.TemplateRevision, false)
		})
	}
	if err != nil {
		h.writeError(w, err)
		return
	}
	if !repeated {
		h.audit(r, claims, "organization.entitlement_applied", request.TemplateKey, request.TemplateRevision, organizationID)
	}
	writeJSON(w, http.StatusOK, applyToJSON(result))
}

func (h *EntitlementTemplatesHandler) HandleGetAccountEntitlement(w http.ResponseWriter, r *http.Request) {
	if !h.requirePlatform(w, r) {
		return
	}
	accountID64, ok := entitlementAccountPathID(w, r)
	if !ok || accountID64 > int64(^uint(0)>>1) {
		return
	}
	item, err := h.store.GetDefaultAccountEntitlement(r.Context(), int(accountID64))
	if err != nil {
		h.writeError(w, err)
		return
	}
	type managedGroup struct {
		ID     int64                 `json:"id"`
		Name   string                `json:"name"`
		Policy entitlementPolicyJSON `json:"policy"`
	}
	var group *managedGroup
	if item.TemplateKey != "" {
		group = &managedGroup{ID: item.GroupID, Name: item.GroupName, Policy: policyToJSON(item.Policy)}
	}
	writeJSON(w, http.StatusOK, struct {
		OrganizationID      uuid.UUID     `json:"organization_id"`
		AccountID           int           `json:"account_id"`
		TemplateKey         string        `json:"template_key"`
		TemplateRevision    int64         `json:"template_revision"`
		ManagedDefaultGroup *managedGroup `json:"managed_default_group"`
		ManagedGroup        *managedGroup `json:"managed_group,omitempty"`
		LibraryIDs          []int         `json:"library_ids"`
		LastReconciledAt    *time.Time    `json:"last_reconciled_at"`
	}{
		OrganizationID: item.OrganizationID, AccountID: item.AccountID,
		TemplateKey: item.TemplateKey, TemplateRevision: item.TemplateRevision,
		ManagedDefaultGroup: group, ManagedGroup: group, LibraryIDs: item.Policy.LibraryIDs,
		LastReconciledAt: item.LastReconciledAt,
	})
}

func (h *EntitlementTemplatesHandler) HandleAccountDryRun(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	accountID64, ok := entitlementAccountPathID(w, r)
	if !ok {
		return
	}
	accountID := int(accountID64)
	var request entitlementApplyRequest
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if !validEntitlementSelection(request) {
		writeAdminValidation(w, map[string]string{"template": "must include key and positive revision"})
		return
	}
	result, err := h.store.ApplyDefaultAccountTemplate(r.Context(), accountID, request.TemplateKey, request.TemplateRevision, true)
	if err != nil {
		h.writeError(w, err)
		return
	}
	expiresAt := h.now().UTC().Add(entitlementConfirmationTTL)
	token, err := h.signConfirmation(entitlementConfirmationClaims{OrganizationID: result.TenantID, TemplateKey: request.TemplateKey, TemplateRevision: request.TemplateRevision, AccountID: claims.AccountID, TargetAccountID: accountID, PreviewHash: entitlementPreviewHash(result), ExpiresAt: expiresAt.Unix()})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", "Entitlement confirmation is unavailable")
		return
	}
	h.writeDryRun(w, result, token, expiresAt)
}

func (h *EntitlementTemplatesHandler) HandleAccountApply(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	accountID64, ok := entitlementAccountPathID(w, r)
	if !ok {
		return
	}
	accountID := int(accountID64)
	var request entitlementApplyRequest
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	request.resolveTokenAlias()
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if !validEntitlementSelection(request) || request.ConfirmationToken == "" || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 200 {
		writeAdminValidation(w, map[string]string{"request": "requires template key/revision, confirmation_token, and idempotency_key"})
		return
	}
	confirmation, err := h.parseConfirmation(request.ConfirmationToken)
	if err != nil || confirmation.TemplateKey != request.TemplateKey || confirmation.TemplateRevision != request.TemplateRevision || confirmation.AccountID != claims.AccountID || confirmation.TargetAccountID != accountID {
		writeAdminValidation(w, map[string]string{"confirmation_token": "is invalid, expired, or belongs to another apply target"})
		return
	}
	var result entitlements.ApplyResult
	var repeated bool
	if atomic, ok := h.store.(entitlementAtomicApplyStore); ok {
		result, repeated, err = atomic.ApplyDefaultAccountTemplateWithReceipt(r.Context(), claims.AccountID, accountID, request.IdempotencyKey, request.TemplateKey, request.TemplateRevision, confirmation.PreviewHash)
	} else {
		receiptKey := strconv.Itoa(claims.AccountID) + ":account:" + strconv.Itoa(accountID) + ":" + request.IdempotencyKey
		result, repeated, err = h.applyOnce(r.Context(), claims.AccountID, "account", strconv.Itoa(accountID), request.IdempotencyKey, receiptKey, request.TemplateKey, request.TemplateRevision, func() (entitlements.ApplyResult, error) {
			preview, previewErr := h.store.ApplyDefaultAccountTemplate(r.Context(), accountID, request.TemplateKey, request.TemplateRevision, true)
			if previewErr != nil {
				return entitlements.ApplyResult{}, previewErr
			}
			if entitlementPreviewHash(preview) != confirmation.PreviewHash {
				return entitlements.ApplyResult{}, errEntitlementConfirmationStale
			}
			return h.store.ApplyDefaultAccountTemplate(r.Context(), accountID, request.TemplateKey, request.TemplateRevision, false)
		})
	}
	if err != nil {
		h.writeError(w, err)
		return
	}
	if result.TenantID != confirmation.OrganizationID {
		writeError(w, http.StatusConflict, "confirmation_target_changed", "Direct account organization changed; dry-run again")
		return
	}
	if !repeated {
		h.audit(r, claims, "account.entitlement_applied", request.TemplateKey, request.TemplateRevision, result.TenantID)
	}
	writeJSON(w, http.StatusOK, applyToJSON(result))
}

func (h *EntitlementTemplatesHandler) applyOnce(ctx context.Context, actorAccountID int, targetType, targetID, idempotencyKey, receiptKey, templateKey string, templateRevision int64, apply func() (entitlements.ApplyResult, error)) (entitlements.ApplyResult, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if durable, ok := h.store.(entitlementReceiptStore); ok {
		prior, found, err := durable.LoadApplyReceipt(ctx, actorAccountID, targetType, targetID, idempotencyKey)
		if err != nil {
			return entitlements.ApplyResult{}, false, err
		}
		if found {
			if prior.TemplateKey != templateKey || prior.TemplateRevision != templateRevision {
				return entitlements.ApplyResult{}, false, entitlements.ErrRevisionConflict
			}
			return prior.Result, true, nil
		}
	}
	if prior, ok := h.receipts[receiptKey]; ok {
		if prior.TemplateKey != templateKey || prior.TemplateRevision != templateRevision {
			return entitlements.ApplyResult{}, false, entitlements.ErrRevisionConflict
		}
		return prior.Result, true, nil
	}
	result, err := apply()
	if err != nil {
		return entitlements.ApplyResult{}, false, err
	}
	if durable, ok := h.store.(entitlementReceiptStore); ok {
		inserted, err := durable.SaveApplyReceipt(ctx, actorAccountID, targetType, targetID, idempotencyKey, templateKey, templateRevision, result)
		if err != nil {
			return entitlements.ApplyResult{}, false, err
		}
		if !inserted {
			prior, found, err := durable.LoadApplyReceipt(ctx, actorAccountID, targetType, targetID, idempotencyKey)
			if err != nil {
				return entitlements.ApplyResult{}, false, err
			}
			if !found {
				return entitlements.ApplyResult{}, false, errors.New("durable entitlement receipt disappeared")
			}
			if prior.TemplateKey != templateKey || prior.TemplateRevision != templateRevision {
				return entitlements.ApplyResult{}, false, entitlements.ErrRevisionConflict
			}
			return prior.Result, true, nil
		}
	}
	h.receipts[receiptKey] = entitlementReceipt{TemplateKey: templateKey, TemplateRevision: templateRevision, Result: result}
	return result, false, nil
}

type entitlementApplyJSON struct {
	OrganizationID           uuid.UUID             `json:"organization_id"`
	AccountID                int                   `json:"account_id,omitempty"`
	TemplateKey              string                `json:"template_key"`
	TemplateRevision         int64                 `json:"template_revision"`
	GroupID                  int64                 `json:"group_id,omitempty"`
	DryRun                   bool                  `json:"dry_run"`
	Changed                  bool                  `json:"changed"`
	ProfilesMoved            int                   `json:"profiles_moved,omitempty"`
	PreviousTemplateKey      string                `json:"previous_template_key,omitempty"`
	PreviousTemplateRevision int64                 `json:"previous_template_revision,omitempty"`
	Policy                   entitlementPolicyJSON `json:"policy"`
}

func applyToJSON(result entitlements.ApplyResult) entitlementApplyJSON {
	return entitlementApplyJSON{OrganizationID: result.TenantID, AccountID: result.AccountID, TemplateKey: result.TemplateKey,
		TemplateRevision: result.TemplateRevision, GroupID: result.GroupID, DryRun: result.DryRun, Changed: result.Changed,
		ProfilesMoved: result.ProfilesMoved, PreviousTemplateKey: result.PreviousTemplateKey,
		PreviousTemplateRevision: result.PreviousTemplateRevision, Policy: policyToJSON(result.Policy)}
}

func (h *EntitlementTemplatesHandler) writeDryRun(w http.ResponseWriter, result entitlements.ApplyResult, token string, expiresAt time.Time) {
	type change struct {
		Field  string `json:"field"`
		Before any    `json:"before"`
		After  any    `json:"after"`
	}
	changes := []change{}
	if result.Changed {
		changes = append(changes, change{
			Field:  "template_revision",
			Before: map[string]any{"template_key": result.PreviousTemplateKey, "template_revision": result.PreviousTemplateRevision},
			After:  map[string]any{"template_key": result.TemplateKey, "template_revision": result.TemplateRevision},
		})
	}
	writeJSON(w, http.StatusOK, struct {
		Result            entitlementApplyJSON `json:"result"`
		TemplateKey       string               `json:"template_key"`
		TemplateRevision  int64                `json:"template_revision"`
		Changed           bool                 `json:"changed"`
		DryRunToken       string               `json:"dry_run_token"`
		ConfirmationToken string               `json:"confirmation_token"`
		ExpiresAt         time.Time            `json:"expires_at"`
		Changes           []change             `json:"changes"`
		Warnings          []string             `json:"warnings"`
	}{
		Result: applyToJSON(result), TemplateKey: result.TemplateKey,
		TemplateRevision: result.TemplateRevision, Changed: result.Changed,
		DryRunToken: token, ConfirmationToken: token, ExpiresAt: expiresAt,
		Changes: changes, Warnings: []string{},
	})
}

func validEntitlementSelection(r entitlementApplyRequest) bool {
	return strings.TrimSpace(r.TemplateKey) != "" && r.TemplateRevision > 0
}

type entitlementConfirmationClaims struct {
	OrganizationID   uuid.UUID `json:"organization_id"`
	TemplateKey      string    `json:"template_key"`
	TemplateRevision int64     `json:"template_revision"`
	AccountID        int       `json:"account_id"`
	TargetAccountID  int       `json:"target_account_id,omitempty"`
	PreviewHash      string    `json:"preview_hash"`
	ExpiresAt        int64     `json:"expires_at"`
}

func entitlementPreviewHash(result entitlements.ApplyResult) string {
	return entitlements.PreviewHash(result)
}

func (h *EntitlementTemplatesHandler) signConfirmation(claims entitlementConfirmationClaims) (string, error) {
	if len(h.secret) == 0 {
		return "", errors.New("confirmation secret unavailable")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, h.secret)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (h *EntitlementTemplatesHandler) parseConfirmation(token string) (entitlementConfirmationClaims, error) {
	var claims entitlementConfirmationClaims
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(h.secret) == 0 {
		return claims, errors.New("invalid token")
	}
	mac := hmac.New(sha256.New, h.secret)
	_, _ = mac.Write([]byte(parts[0]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return claims, errors.New("invalid token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(payload, &claims) != nil || claims.ExpiresAt <= h.now().UTC().Unix() {
		return claims, errors.New("expired token")
	}
	return claims, nil
}

func (h *EntitlementTemplatesHandler) requirePlatform(w http.ResponseWriter, r *http.Request) bool {
	claims, ok := middleware.GetAdminContextClaims(r.Context())
	if !ok || claims.Scope != auth.AdminScopePlatform || claims.AccountID <= 0 {
		writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required")
		return false
	}
	if h == nil || h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", "Entitlement templates are unavailable")
		return false
	}
	return true
}

func (h *EntitlementTemplatesHandler) requirePlatformMutation(w http.ResponseWriter, r *http.Request) (auth.AdminContextClaims, bool) {
	if !h.requirePlatform(w, r) {
		return auth.AdminContextClaims{}, false
	}
	claims, _ := middleware.GetAdminContextClaims(r.Context())
	return claims, true
}

func (h *EntitlementTemplatesHandler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, entitlements.ErrTemplateNotFound), errors.Is(err, entitlements.ErrTenantNotFound), errors.Is(err, entitlements.ErrAccountNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Entitlement resource not found")
	case errors.Is(err, entitlements.ErrTemplateUnavailable):
		writeError(w, http.StatusUnprocessableEntity, "template_unavailable", "Entitlement template revision is unavailable")
	case errors.Is(err, entitlements.ErrTemplateDuplicate):
		writeError(w, http.StatusConflict, "template_conflict", "Entitlement template key or name already exists")
	case errors.Is(err, entitlements.ErrRevisionConflict):
		writeError(w, http.StatusConflict, "revision_conflict", "Entitlement template revision changed; reload and retry")
	case errors.Is(err, entitlements.ErrInvalidPolicy):
		writeAdminValidation(w, map[string]string{"policy": err.Error()})
	case errors.Is(err, entitlements.ErrProtectedTemplate):
		writeError(w, http.StatusConflict, "protected_template", "The Browse-only authorization boundary cannot be weakened or archived")
	case errors.Is(err, errEntitlementConfirmationStale), errors.Is(err, entitlements.ErrConfirmationStale):
		writeError(w, http.StatusConflict, "confirmation_stale", "Entitlement state changed; dry-run again")
	default:
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", "Entitlement administration is unavailable")
	}
}

func (h *EntitlementTemplatesHandler) audit(r *http.Request, claims auth.AdminContextClaims, action, key string, revision int64, organizationID uuid.UUID) {
	attrs := []any{"component", "entitlements", "action", action, "actor_account_id", claims.AccountID, "template_key", key, "template_revision", revision, "request_id", adminRequestID(r)}
	if organizationID != uuid.Nil {
		attrs = append(attrs, "organization_id", organizationID.String())
	}
	slog.InfoContext(r.Context(), "entitlement administration mutation", attrs...)
	if durable, ok := h.store.(entitlementAuditStore); ok {
		if err := durable.RecordAudit(r.Context(), entitlements.AuditEvent{
			ActorAccountID: claims.AccountID, Action: action, OrganizationID: organizationID,
			TemplateKey: key, TemplateRevision: revision, RequestID: adminRequestID(r),
		}); err != nil {
			slog.ErrorContext(r.Context(), "failed to persist entitlement audit event", "component", "entitlements", "action", action, "error", err)
		}
	}
}
