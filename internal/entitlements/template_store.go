// Package entitlements owns revisioned per-member policy templates and their
// materialization into tenant-managed default access groups.
package entitlements

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrTemplateNotFound reports an unknown key or revision.
	ErrTemplateNotFound = errors.New("entitlements: template not found")
	// ErrTemplateUnavailable reports a disabled or archived template.
	ErrTemplateUnavailable = errors.New("entitlements: template is unavailable")
	// ErrTemplateDuplicate reports a duplicate key or display name.
	ErrTemplateDuplicate = errors.New("entitlements: template key or name already exists")
	// ErrRevisionConflict reports an optimistic revision mismatch.
	ErrRevisionConflict  = errors.New("entitlements: template revision conflict")
	ErrConfirmationStale = errors.New("entitlements: confirmation is stale")
	// ErrInvalidPolicy reports a locally invalid template policy.
	ErrInvalidPolicy = errors.New("entitlements: invalid policy")
	// ErrProtectedTemplate reports an attempt to remove or weaken a built-in
	// authorization boundary.
	ErrProtectedTemplate = errors.New("entitlements: protected template")
	// ErrTenantNotFound reports an unknown or non-Park tenant organization.
	ErrTenantNotFound = errors.New("entitlements: tenant not found")
	// ErrAccountNotFound reports an account outside the asserted organization.
	ErrAccountNotFound = errors.New("entitlements: account not found")
)

var templateKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

// Policy is the per-member policy captured by an immutable template revision.
// A nil LibraryIDs slice means all libraries enabled at materialization time;
// a non-nil empty slice means no libraries.
type Policy struct {
	LibraryIDs               []int
	PlaybackAllowed          bool
	MaxStreams               int
	MaxProfiles              int
	TranscodeAllowed         bool
	MaxTranscodes            int
	DownloadAllowed          bool
	DownloadTranscodeAllowed bool
	MaxPlaybackQuality       string
	AllowedPermissions       []string
	RequestsAllowed          bool
}

// Template combines stable template identity state with one immutable policy
// revision.
type Template struct {
	Key       string
	Name      string
	Revision  int64
	Enabled   bool
	Archived  bool
	Policy    Policy
	CreatedAt time.Time
}

// CreateTemplateInput creates revision 1 of a new stable template key.
type CreateTemplateInput struct {
	Key     string
	Name    string
	Enabled bool
	Policy  Policy
}

// ReviseTemplateInput appends a policy revision and updates canonical display
// and enabled state. History is never rewritten.
type ReviseTemplateInput struct {
	Name    string
	Enabled bool
	Policy  Policy
}

// ApplyResult describes the effective materialization. Dry-run results contain
// the same diff but GroupID is zero when a managed group would be created.
type ApplyResult struct {
	TenantID                 uuid.UUID
	AccountID                int
	TemplateKey              string
	TemplateRevision         int64
	GroupID                  int64
	DryRun                   bool
	Changed                  bool
	ProfilesMoved            int
	PreviousTemplateKey      string
	PreviousTemplateRevision int64
	Policy                   Policy
}

type AuditEvent struct {
	ID               int64     `json:"id"`
	CreatedAt        time.Time `json:"created_at"`
	ActorAccountID   int       `json:"actor_account_id,omitempty"`
	Action           string    `json:"action"`
	OrganizationID   uuid.UUID `json:"organization_id,omitempty"`
	TargetAccountID  int       `json:"target_account_id,omitempty"`
	TemplateKey      string    `json:"template_key,omitempty"`
	TemplateRevision int64     `json:"template_revision,omitempty"`
	RequestID        string    `json:"request_id,omitempty"`
}

type ApplyReceipt struct {
	TemplateKey      string
	TemplateRevision int64
	Result           ApplyResult
}

func (s *Store) RecordAudit(ctx context.Context, event AuditEvent) error {
	var organizationID any
	if event.OrganizationID != uuid.Nil {
		organizationID = event.OrganizationID
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO entitlement_audit_events (
			actor_account_id,action,organization_id,target_account_id,
			template_key,template_revision,request_id
		) VALUES (NULLIF($1,0),$2,$3,NULLIF($4,0),NULLIF($5,''),NULLIF($6,0),NULLIF($7,''))`,
		event.ActorAccountID, event.Action, organizationID, event.TargetAccountID,
		event.TemplateKey, event.TemplateRevision, event.RequestID)
	if err != nil {
		return fmt.Errorf("entitlements: record audit event: %w", err)
	}
	return nil
}

func (s *Store) ListOrganizationAudit(ctx context.Context, organizationID uuid.UUID) ([]AuditEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,created_at,COALESCE(actor_account_id,0),action,organization_id,
		       COALESCE(target_account_id,0),COALESCE(template_key,''),
		       COALESCE(template_revision,0),COALESCE(request_id,'')
		FROM entitlement_audit_events WHERE organization_id=$1
		ORDER BY created_at DESC,id DESC LIMIT 200`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("entitlements: list audit events: %w", err)
	}
	defer rows.Close()
	events := []AuditEvent{}
	for rows.Next() {
		var event AuditEvent
		if err := rows.Scan(&event.ID, &event.CreatedAt, &event.ActorAccountID, &event.Action, &event.OrganizationID,
			&event.TargetAccountID, &event.TemplateKey, &event.TemplateRevision, &event.RequestID); err != nil {
			return nil, fmt.Errorf("entitlements: scan audit event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) LoadApplyReceipt(ctx context.Context, actorAccountID int, targetType, targetID, idempotencyKey string) (ApplyReceipt, bool, error) {
	var receipt ApplyReceipt
	var payload []byte
	err := s.pool.QueryRow(ctx, `SELECT template_key,template_revision,result FROM entitlement_apply_receipts
		WHERE actor_account_id=$1 AND target_type=$2 AND target_id=$3 AND idempotency_key=$4`,
		actorAccountID, targetType, targetID, idempotencyKey).Scan(&receipt.TemplateKey, &receipt.TemplateRevision, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplyReceipt{}, false, nil
	}
	if err != nil {
		return ApplyReceipt{}, false, fmt.Errorf("entitlements: load apply receipt: %w", err)
	}
	if err := json.Unmarshal(payload, &receipt.Result); err != nil {
		return ApplyReceipt{}, false, fmt.Errorf("entitlements: decode apply receipt: %w", err)
	}
	return receipt, true, nil
}

func (s *Store) SaveApplyReceipt(ctx context.Context, actorAccountID int, targetType, targetID, idempotencyKey, templateKey string, templateRevision int64, result ApplyResult) (bool, error) {
	payload, err := json.Marshal(result)
	if err != nil {
		return false, fmt.Errorf("entitlements: encode apply receipt: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO entitlement_apply_receipts
		(actor_account_id,target_type,target_id,idempotency_key,template_key,template_revision,result)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, actorAccountID, targetType, targetID, idempotencyKey, templateKey, templateRevision, payload)
	if err != nil {
		return false, fmt.Errorf("entitlements: save apply receipt: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PreviewHash is the canonical state binding used by dry-run confirmations.
func PreviewHash(result ApplyResult) string {
	result.DryRun = false
	payload, _ := json.Marshal(result)
	sum := sha256.Sum256(payload)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ApplyTemplateWithReceipt serializes an idempotency key across replicas and
// commits the materialization and its exact response receipt together.
func (s *Store) ApplyTemplateWithReceipt(ctx context.Context, actorAccountID int, organizationID uuid.UUID, idempotencyKey, templateKey string, templateRevision int64, previewHash string) (ApplyResult, bool, error) {
	return s.applyWithReceipt(ctx, actorAccountID, "organization", organizationID.String(), idempotencyKey, templateKey, templateRevision, previewHash,
		func(tx pgx.Tx, dryRun bool) (ApplyResult, error) {
			return ApplyTemplateInTx(ctx, tx, organizationID, templateKey, templateRevision, dryRun)
		})
}

// ApplyDefaultAccountTemplateWithReceipt is the direct-account equivalent of
// ApplyTemplateWithReceipt, including default-organization resolution inside
// the same transaction.
func (s *Store) ApplyDefaultAccountTemplateWithReceipt(ctx context.Context, actorAccountID, accountID int, idempotencyKey, templateKey string, templateRevision int64, previewHash string) (ApplyResult, bool, error) {
	return s.applyWithReceipt(ctx, actorAccountID, "account", fmt.Sprint(accountID), idempotencyKey, templateKey, templateRevision, previewHash,
		func(tx pgx.Tx, dryRun bool) (ApplyResult, error) {
			var organizationID uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT o.id FROM organizations o JOIN organization_memberships m ON m.organization_id=o.id WHERE o.is_default AND m.account_id=$1 AND m.status='active'`, accountID).Scan(&organizationID); errors.Is(err, pgx.ErrNoRows) {
				return ApplyResult{}, ErrAccountNotFound
			} else if err != nil {
				return ApplyResult{}, fmt.Errorf("entitlements: resolve direct account organization: %w", err)
			}
			return applyAccountTemplateInTx(ctx, tx, organizationID, accountID, templateKey, templateRevision, dryRun)
		})
}

func (s *Store) applyWithReceipt(ctx context.Context, actorAccountID int, targetType, targetID, idempotencyKey, templateKey string, templateRevision int64, previewHash string, apply func(pgx.Tx, bool) (ApplyResult, error)) (ApplyResult, bool, error) {
	lockKey := fmt.Sprintf("%d:%s:%s:%s", actorAccountID, targetType, targetID, idempotencyKey)
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return ApplyResult{}, false, fmt.Errorf("entitlements: acquire atomic apply connection: %w", err)
	}
	defer conn.Release()
	// Take the cross-replica lock before starting REPEATABLE READ. Otherwise a
	// waiter establishes a stale snapshot while blocked on the advisory lock and
	// cannot observe the receipt committed by the winner.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return ApplyResult{}, false, fmt.Errorf("entitlements: lock apply receipt: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1,0))`, lockKey)
	}()
	// The confirmed preview and the write must resolve dynamic all-library
	// policy from one database snapshot. READ COMMITTED could observe a library
	// toggle between those statements and materialize unconfirmed access.
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return ApplyResult{}, false, fmt.Errorf("entitlements: begin atomic apply: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var prior ApplyReceipt
	var payload []byte
	err = tx.QueryRow(ctx, `SELECT template_key,template_revision,result FROM entitlement_apply_receipts WHERE actor_account_id=$1 AND target_type=$2 AND target_id=$3 AND idempotency_key=$4 FOR UPDATE`, actorAccountID, targetType, targetID, idempotencyKey).Scan(&prior.TemplateKey, &prior.TemplateRevision, &payload)
	if err == nil {
		if prior.TemplateKey != templateKey || prior.TemplateRevision != templateRevision {
			return ApplyResult{}, false, ErrRevisionConflict
		}
		if err := json.Unmarshal(payload, &prior.Result); err != nil {
			return ApplyResult{}, false, fmt.Errorf("entitlements: decode apply receipt: %w", err)
		}
		return prior.Result, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ApplyResult{}, false, fmt.Errorf("entitlements: load atomic apply receipt: %w", err)
	}
	preview, err := apply(tx, true)
	if err != nil {
		return ApplyResult{}, false, err
	}
	if PreviewHash(preview) != previewHash {
		return ApplyResult{}, false, ErrConfirmationStale
	}
	result, err := apply(tx, false)
	if err != nil {
		return ApplyResult{}, false, err
	}
	payload, err = json.Marshal(result)
	if err != nil {
		return ApplyResult{}, false, fmt.Errorf("entitlements: encode atomic apply receipt: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entitlement_apply_receipts (actor_account_id,target_type,target_id,idempotency_key,template_key,template_revision,result) VALUES ($1,$2,$3,$4,$5,$6,$7)`, actorAccountID, targetType, targetID, idempotencyKey, templateKey, templateRevision, payload); err != nil {
		return ApplyResult{}, false, fmt.Errorf("entitlements: save atomic apply receipt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplyResult{}, false, fmt.Errorf("entitlements: commit atomic apply: %w", err)
	}
	return result, false, nil
}

// OrganizationEntitlement is the operator-facing projection of a Park
// tenant's managed default group and separate tenant-wide quota layer.
type OrganizationEntitlement struct {
	OrganizationID   uuid.UUID
	TemplateKey      string
	TemplateRevision int64
	GroupID          int64
	GroupName        string
	Policy           Policy
	Slots            int
	Transcodes       int
	LastReconciledAt *time.Time
}

// AccountEntitlement is the current direct-product template group selected by
// one account in the deployment default organization.
type AccountEntitlement struct {
	OrganizationID   uuid.UUID
	AccountID        int
	TemplateKey      string
	TemplateRevision int64
	GroupID          int64
	GroupName        string
	Policy           Policy
	LastReconciledAt *time.Time
}

func (s *Store) GetDefaultAccountEntitlement(ctx context.Context, accountID int) (AccountEntitlement, error) {
	var result AccountEntitlement
	result.AccountID = accountID
	var reconciled *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT o.id,u.access_group_id,COALESCE(g.name,''),COALESCE(g.managed_template_key,''),
		       COALESCE(g.managed_template_revision,0),g.library_ids,g.playback_allowed,
		       g.max_streams,g.max_profiles,g.transcode_allowed,g.max_transcodes,
		       g.download_allowed,g.download_transcode_allowed,g.max_playback_quality,
		       g.allowed_permissions,g.requests_allowed,g.updated_at
		FROM users u
		JOIN organization_memberships m ON m.account_id=u.id AND m.status='active'
		JOIN organizations o ON o.id=m.organization_id AND o.is_default
		JOIN access_groups g ON g.organization_id=o.id AND g.id=u.access_group_id
		WHERE u.id=$1`, accountID).Scan(
		&result.OrganizationID, &result.GroupID, &result.GroupName, &result.TemplateKey,
		&result.TemplateRevision, &result.Policy.LibraryIDs, &result.Policy.PlaybackAllowed,
		&result.Policy.MaxStreams, &result.Policy.MaxProfiles, &result.Policy.TranscodeAllowed,
		&result.Policy.MaxTranscodes, &result.Policy.DownloadAllowed,
		&result.Policy.DownloadTranscodeAllowed, &result.Policy.MaxPlaybackQuality,
		&result.Policy.AllowedPermissions, &result.Policy.RequestsAllowed, &reconciled,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccountEntitlement{}, ErrAccountNotFound
	}
	if err != nil {
		return AccountEntitlement{}, fmt.Errorf("entitlements: load direct account entitlement: %w", err)
	}
	if result.TemplateKey != "" {
		result.LastReconciledAt = reconciled
	}
	return result, nil
}

// GetOrganizationEntitlement loads one Park tenant without exposing custom
// groups. A tenant that has not yet been reconciled returns zero managed-group
// fields alongside its quota layer.
func (s *Store) GetOrganizationEntitlement(ctx context.Context, organizationID uuid.UUID) (OrganizationEntitlement, error) {
	var result OrganizationEntitlement
	result.OrganizationID = organizationID
	if err := s.pool.QueryRow(ctx, `
		SELECT slots,transcodes FROM organizations
		WHERE id=$1 AND external_service_id IS NOT NULL`, organizationID).Scan(&result.Slots, &result.Transcodes); errors.Is(err, pgx.ErrNoRows) {
		return OrganizationEntitlement{}, ErrTenantNotFound
	} else if err != nil {
		return OrganizationEntitlement{}, fmt.Errorf("entitlements: load tenant quota layer: %w", err)
	}
	var reconciled time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id,name,managed_template_key,managed_template_revision,
		       library_ids,playback_allowed,max_streams,max_profiles,
		       transcode_allowed,max_transcodes,download_allowed,
		       download_transcode_allowed,max_playback_quality,
		       allowed_permissions,requests_allowed,updated_at
		FROM access_groups
		WHERE organization_id=$1 AND is_default AND managed_template_key IS NOT NULL`, organizationID).Scan(
		&result.GroupID, &result.GroupName, &result.TemplateKey, &result.TemplateRevision,
		&result.Policy.LibraryIDs, &result.Policy.PlaybackAllowed, &result.Policy.MaxStreams,
		&result.Policy.MaxProfiles, &result.Policy.TranscodeAllowed, &result.Policy.MaxTranscodes,
		&result.Policy.DownloadAllowed, &result.Policy.DownloadTranscodeAllowed,
		&result.Policy.MaxPlaybackQuality, &result.Policy.AllowedPermissions,
		&result.Policy.RequestsAllowed, &reconciled,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return OrganizationEntitlement{}, fmt.Errorf("entitlements: load tenant managed entitlement: %w", err)
	}
	result.LastReconciledAt = &reconciled
	return result, nil
}

// ApplyAccountTemplate materializes/reuses an organization-scoped immutable
// template group and makes it the entitlement group for one direct account.
// Profiles in the account's prior managed group or the organization default
// follow the entitlement; deliberately custom-group profiles are preserved.
func (s *Store) ApplyAccountTemplate(ctx context.Context, organizationID uuid.UUID, accountID int, key string, revision int64, dryRun bool) (result ApplyResult, err error) {
	if organizationID == uuid.Nil || accountID <= 0 || revision <= 0 {
		return ApplyResult{}, fmt.Errorf("%w: organization, account, and revision are required", ErrInvalidPolicy)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: begin account template apply: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err = applyAccountTemplateInTx(ctx, tx, organizationID, accountID, key, revision, dryRun)
	if err != nil || dryRun {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: commit account template apply: %w", err)
	}
	return result, nil
}

func applyAccountTemplateInTx(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int, key string, revision int64, dryRun bool) (result ApplyResult, err error) {

	if err := tx.QueryRow(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, organizationID).Scan(&organizationID); errors.Is(err, pgx.ErrNoRows) {
		return ApplyResult{}, ErrAccountNotFound
	} else if err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: lock account organization: %w", err)
	}
	var priorGroupID *int64
	if err := tx.QueryRow(ctx, `
		SELECT u.access_group_id
		FROM users u
		JOIN organization_memberships m ON m.account_id=u.id
		WHERE u.id=$1 AND m.organization_id=$2 AND m.status='active'
		FOR UPDATE OF u`, accountID, organizationID).Scan(&priorGroupID); errors.Is(err, pgx.ErrNoRows) {
		return ApplyResult{}, ErrAccountNotFound
	} else if err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: lock direct account: %w", err)
	}
	template, err := getTemplate(ctx, tx, strings.TrimSpace(key), revision, false)
	if err != nil {
		return ApplyResult{}, err
	}
	if !template.Enabled || template.Archived {
		return ApplyResult{}, ErrTemplateUnavailable
	}
	effectivePolicy, err := resolveMaterializedPolicy(ctx, tx, template.Policy)
	if err != nil {
		return ApplyResult{}, err
	}

	result = ApplyResult{
		TenantID: organizationID, AccountID: accountID, TemplateKey: template.Key,
		TemplateRevision: template.Revision, DryRun: dryRun, Policy: effectivePolicy,
	}
	var targetPolicy Policy
	err = tx.QueryRow(ctx, `
		SELECT id, library_ids, playback_allowed, max_streams, max_profiles,
		       transcode_allowed, max_transcodes, download_allowed,
		       download_transcode_allowed, max_playback_quality,
		       allowed_permissions, requests_allowed
		FROM access_groups
		WHERE organization_id=$1 AND managed_template_key=$2 AND managed_template_revision=$3
		  AND NOT is_default
		FOR UPDATE`, organizationID, template.Key, template.Revision).Scan(
		&result.GroupID, &targetPolicy.LibraryIDs, &targetPolicy.PlaybackAllowed,
		&targetPolicy.MaxStreams, &targetPolicy.MaxProfiles, &targetPolicy.TranscodeAllowed,
		&targetPolicy.MaxTranscodes, &targetPolicy.DownloadAllowed,
		&targetPolicy.DownloadTranscodeAllowed, &targetPolicy.MaxPlaybackQuality,
		&targetPolicy.AllowedPermissions, &targetPolicy.RequestsAllowed,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ApplyResult{}, fmt.Errorf("entitlements: load direct template group: %w", err)
	}
	groupExists := err == nil

	var defaultGroupID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, organizationID).Scan(&defaultGroupID); err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: load organization default group: %w", err)
	}
	moveGroupIDs := []int64{defaultGroupID}
	if priorGroupID != nil {
		var priorDefault bool
		var priorKey *string
		var priorRevision *int64
		if err := tx.QueryRow(ctx, `
			SELECT is_default, managed_template_key, managed_template_revision
			FROM access_groups WHERE organization_id=$1 AND id=$2`, organizationID, *priorGroupID).
			Scan(&priorDefault, &priorKey, &priorRevision); err != nil {
			return ApplyResult{}, fmt.Errorf("entitlements: load prior account group: %w", err)
		}
		if priorKey != nil {
			result.PreviousTemplateKey = *priorKey
			result.PreviousTemplateRevision = *priorRevision
		}
		if (priorDefault || priorKey != nil) && (!groupExists || *priorGroupID != result.GroupID) {
			moveGroupIDs = append(moveGroupIDs, *priorGroupID)
		}
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)::int FROM user_profiles
		WHERE user_id=$1 AND organization_id=$2 AND access_group_id=ANY($3)`, accountID, organizationID, moveGroupIDs).
		Scan(&result.ProfilesMoved); err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: count direct profiles to reconcile: %w", err)
	}
	result.Changed = !groupExists || !policiesEqual(targetPolicy, effectivePolicy) || priorGroupID == nil || *priorGroupID != result.GroupID || result.ProfilesMoved > 0
	if dryRun || !result.Changed {
		return result, nil
	}

	if !groupExists {
		if err := tx.QueryRow(ctx, `
			INSERT INTO access_groups (
				organization_id,name,description,is_default,library_ids,max_playback_quality,
				playback_allowed,download_allowed,download_transcode_allowed,max_streams,
				max_profiles,transcode_allowed,max_transcodes,allowed_permissions,
				requests_allowed,managed_template_key,managed_template_revision
			) VALUES ($1,$2,'Managed direct-account Vondel entitlement.',false,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			RETURNING id`, organizationID, "Managed Entitlement "+template.Key+" r"+fmt.Sprint(template.Revision),
			effectivePolicy.LibraryIDs, effectivePolicy.MaxPlaybackQuality,
			effectivePolicy.PlaybackAllowed, effectivePolicy.DownloadAllowed,
			effectivePolicy.DownloadTranscodeAllowed, effectivePolicy.MaxStreams,
			effectivePolicy.MaxProfiles, effectivePolicy.TranscodeAllowed,
			effectivePolicy.MaxTranscodes, effectivePolicy.AllowedPermissions,
			effectivePolicy.RequestsAllowed, template.Key, template.Revision).Scan(&result.GroupID); err != nil {
			return ApplyResult{}, fmt.Errorf("entitlements: create direct template group: %w", err)
		}
	} else if !policiesEqual(targetPolicy, effectivePolicy) {
		if _, err := tx.Exec(ctx, `
			UPDATE access_groups SET library_ids=$4,max_playback_quality=$5,playback_allowed=$6,
				download_allowed=$7,download_transcode_allowed=$8,max_streams=$9,max_profiles=$10,
				transcode_allowed=$11,max_transcodes=$12,allowed_permissions=$13,requests_allowed=$14,updated_at=now()
			WHERE organization_id=$1 AND id=$2 AND managed_template_key=$3`,
			organizationID, result.GroupID, template.Key, effectivePolicy.LibraryIDs,
			effectivePolicy.MaxPlaybackQuality, effectivePolicy.PlaybackAllowed,
			effectivePolicy.DownloadAllowed, effectivePolicy.DownloadTranscodeAllowed,
			effectivePolicy.MaxStreams, effectivePolicy.MaxProfiles, effectivePolicy.TranscodeAllowed,
			effectivePolicy.MaxTranscodes, effectivePolicy.AllowedPermissions, effectivePolicy.RequestsAllowed); err != nil {
			return ApplyResult{}, fmt.Errorf("entitlements: refresh direct template group: %w", err)
		}
		// Exact-revision groups are shared. Dynamic all-libraries resolution can
		// change the group policy, so invalidate every other account using it.
		rows, err := tx.Query(ctx, `
			UPDATE users SET access_policy_revision=access_policy_revision+1,updated_at=now()
			WHERE access_group_id=$1 AND id<>$2 RETURNING id`, result.GroupID, accountID)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("entitlements: invalidate shared direct group members: %w", err)
		}
		var affectedAccounts []int
		for rows.Next() {
			var affectedID int
			if err := rows.Scan(&affectedID); err != nil {
				rows.Close()
				return ApplyResult{}, fmt.Errorf("entitlements: scan shared direct group member: %w", err)
			}
			affectedAccounts = append(affectedAccounts, affectedID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return ApplyResult{}, fmt.Errorf("entitlements: iterate shared direct group members: %w", err)
		}
		rows.Close()
		for _, affectedID := range affectedAccounts {
			if _, err := tx.Exec(ctx, `INSERT INTO entitlement_audit_events
				(action,organization_id,target_account_id,template_key,template_revision)
				VALUES ('account.entitlement_shared_policy_changed',$1,$2,$3,$4)`, organizationID, affectedID, template.Key, template.Revision); err != nil {
				return ApplyResult{}, fmt.Errorf("entitlements: audit shared direct group member: %w", err)
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE user_profiles SET access_group_id=$4,updated_at=now()
		WHERE user_id=$1 AND organization_id=$2 AND access_group_id=ANY($3)`, accountID, organizationID, moveGroupIDs, result.GroupID); err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: reconcile direct profiles: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET access_group_id=$2,access_policy_revision=access_policy_revision+1,updated_at=now()
		WHERE id=$1`, accountID, result.GroupID); err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: assign direct account template: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entitlement_audit_events
		(action,organization_id,target_account_id,template_key,template_revision)
		VALUES ('account.entitlement_materialized',$1,$2,$3,$4)`, organizationID, accountID, template.Key, template.Revision); err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: audit direct account materialization: %w", err)
	}
	return result, nil
}

// ApplyDefaultAccountTemplate is the direct-product provisioning shortcut:
// direct accounts live in the deployment default organization, while Park
// tenant accounts use ApplyTemplate on their tenant organization.
func (s *Store) ApplyDefaultAccountTemplate(ctx context.Context, accountID int, key string, revision int64, dryRun bool) (ApplyResult, error) {
	var organizationID uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		SELECT o.id
		FROM organizations o
		JOIN organization_memberships m ON m.organization_id=o.id
		WHERE o.is_default AND m.account_id=$1 AND m.status='active'`, accountID).Scan(&organizationID); errors.Is(err, pgx.ErrNoRows) {
		return ApplyResult{}, ErrAccountNotFound
	} else if err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: resolve direct account organization: %w", err)
	}
	return s.ApplyAccountTemplate(ctx, organizationID, accountID, key, revision, dryRun)
}

// Store persists templates and materializes them into tenant access groups.
type Store struct {
	pool *pgxpool.Pool
}

// NewTemplateStore creates a PostgreSQL-backed template store.
func NewTemplateStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ValidatePolicy validates relationships the database also enforces.
func ValidatePolicy(policy Policy) error {
	if policy.MaxStreams < 0 || policy.MaxProfiles < 0 || policy.MaxTranscodes < 0 {
		return fmt.Errorf("%w: limits cannot be negative", ErrInvalidPolicy)
	}
	if policy.DownloadTranscodeAllowed && !policy.DownloadAllowed {
		return fmt.Errorf("%w: transcoded downloads require downloads", ErrInvalidPolicy)
	}
	if !policy.PlaybackAllowed && (policy.MaxStreams != 0 || policy.TranscodeAllowed || policy.MaxTranscodes != 0 || policy.DownloadAllowed || policy.DownloadTranscodeAllowed) {
		return fmt.Errorf("%w: playback-disabled policies cannot allow streams, transcodes, or downloads", ErrInvalidPolicy)
	}
	for _, id := range policy.LibraryIDs {
		if id <= 0 {
			return fmt.Errorf("%w: library ids must be positive", ErrInvalidPolicy)
		}
	}
	return nil
}

func normalizePolicy(policy Policy) (Policy, error) {
	if err := ValidatePolicy(policy); err != nil {
		return Policy{}, err
	}
	policy.MaxPlaybackQuality = normalizePlaybackQuality(policy.MaxPlaybackQuality)
	if policy.LibraryIDs != nil {
		policy.LibraryIDs = append([]int(nil), policy.LibraryIDs...)
		sort.Ints(policy.LibraryIDs)
		out := policy.LibraryIDs[:0]
		for _, id := range policy.LibraryIDs {
			if len(out) == 0 || out[len(out)-1] != id {
				out = append(out, id)
			}
		}
		policy.LibraryIDs = out
	}
	if policy.AllowedPermissions != nil {
		policy.AllowedPermissions = append([]string(nil), policy.AllowedPermissions...)
		for index := range policy.AllowedPermissions {
			policy.AllowedPermissions[index] = strings.TrimSpace(policy.AllowedPermissions[index])
		}
		sort.Strings(policy.AllowedPermissions)
		out := policy.AllowedPermissions[:0]
		for _, permission := range policy.AllowedPermissions {
			if permission != "" && (len(out) == 0 || out[len(out)-1] != permission) {
				out = append(out, permission)
			}
		}
		policy.AllowedPermissions = out
	}
	return policy, nil
}

// Create inserts a new stable key and its first immutable revision.
func (s *Store) Create(ctx context.Context, input CreateTemplateInput) (template Template, err error) {
	input.Key = strings.TrimSpace(input.Key)
	input.Name = strings.TrimSpace(input.Name)
	if !templateKeyPattern.MatchString(input.Key) || input.Name == "" {
		return Template{}, fmt.Errorf("%w: key and name are required", ErrInvalidPolicy)
	}
	input.Policy, err = normalizePolicy(input.Policy)
	if err != nil {
		return Template{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Template{}, fmt.Errorf("entitlements: begin template create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `
		INSERT INTO entitlement_templates (key, name, current_revision, enabled)
		VALUES ($1, $2, 1, $3)`, input.Key, input.Name, input.Enabled); err != nil {
		if isDuplicate(err) {
			return Template{}, ErrTemplateDuplicate
		}
		return Template{}, fmt.Errorf("entitlements: create template identity: %w", err)
	}
	if err = insertRevision(ctx, tx, input.Key, 1, input.Policy); err != nil {
		return Template{}, err
	}
	template, err = getTemplate(ctx, tx, input.Key, 1, false)
	if err != nil {
		return Template{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO entitlement_audit_events
		(action,template_key,template_revision) VALUES ('entitlement_template.created',$1,1)`, input.Key); err != nil {
		return Template{}, fmt.Errorf("entitlements: audit template create: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Template{}, fmt.Errorf("entitlements: commit template create: %w", err)
	}
	return template, nil
}

// Get loads an exact immutable policy revision with current identity state.
func (s *Store) Get(ctx context.Context, key string, revision int64) (Template, error) {
	return getTemplate(ctx, s.pool, strings.TrimSpace(key), revision, false)
}

// Latest loads the current revision for a stable key.
func (s *Store) Latest(ctx context.Context, key string) (Template, error) {
	return getTemplate(ctx, s.pool, strings.TrimSpace(key), 0, true)
}

// List returns current revisions, excluding archived identities unless asked.
func (s *Store) List(ctx context.Context, includeArchived bool) ([]Template, error) {
	query := `
		SELECT t.key, t.name, r.revision, t.enabled, t.archived,
		       r.library_ids, r.playback_allowed, r.max_streams, r.max_profiles,
		       r.transcode_allowed, r.max_transcodes, r.download_allowed,
		       r.download_transcode_allowed, r.max_playback_quality,
		       r.allowed_permissions, r.requests_allowed, r.created_at
		FROM entitlement_templates t
		JOIN entitlement_template_revisions r
		  ON r.template_key=t.key AND r.revision=t.current_revision`
	if !includeArchived {
		query += ` WHERE NOT t.archived`
	}
	query += ` ORDER BY lower(t.name), t.key`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("entitlements: list templates: %w", err)
	}
	defer rows.Close()
	result := []Template{}
	for rows.Next() {
		template, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("entitlements: scan template list: %w", err)
		}
		result = append(result, template)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entitlements: iterate templates: %w", err)
	}
	return result, nil
}

// ListRevisions returns the immutable history for one stable key, newest first.
func (s *Store) ListRevisions(ctx context.Context, key string) ([]Template, error) {
	key = strings.TrimSpace(key)
	rows, err := s.pool.Query(ctx, `
		SELECT t.key,t.name,r.revision,t.enabled,t.archived,
		       r.library_ids,r.playback_allowed,r.max_streams,r.max_profiles,
		       r.transcode_allowed,r.max_transcodes,r.download_allowed,
		       r.download_transcode_allowed,r.max_playback_quality,
		       r.allowed_permissions,r.requests_allowed,r.created_at
		FROM entitlement_templates t
		JOIN entitlement_template_revisions r ON r.template_key=t.key
		WHERE t.key=$1 ORDER BY r.revision DESC`, key)
	if err != nil {
		return nil, fmt.Errorf("entitlements: list template revisions: %w", err)
	}
	defer rows.Close()
	result := []Template{}
	for rows.Next() {
		item, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("entitlements: scan template revision: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entitlements: iterate template revisions: %w", err)
	}
	if len(result) == 0 {
		return nil, ErrTemplateNotFound
	}
	return result, nil
}

// Revise appends a policy revision after checking the caller's optimistic
// expected revision.
func (s *Store) Revise(ctx context.Context, key string, expectedRevision int64, input ReviseTemplateInput) (template Template, err error) {
	key = strings.TrimSpace(key)
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || expectedRevision <= 0 {
		return Template{}, fmt.Errorf("%w: name and expected revision are required", ErrInvalidPolicy)
	}
	input.Policy, err = normalizePolicy(input.Policy)
	if err != nil {
		return Template{}, err
	}
	if key == "browse-only" && input.Policy.PlaybackAllowed {
		return Template{}, ErrProtectedTemplate
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Template{}, fmt.Errorf("entitlements: begin template revision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current int64
	var archived bool
	if err = tx.QueryRow(ctx, `
		SELECT current_revision, archived FROM entitlement_templates
		WHERE key=$1 FOR UPDATE`, key).Scan(&current, &archived); errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrTemplateNotFound
	} else if err != nil {
		return Template{}, fmt.Errorf("entitlements: lock template: %w", err)
	}
	if archived {
		return Template{}, ErrTemplateUnavailable
	}
	if current != expectedRevision {
		return Template{}, ErrRevisionConflict
	}
	next := current + 1
	if err = insertRevision(ctx, tx, key, next, input.Policy); err != nil {
		return Template{}, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE entitlement_templates
		SET name=$2, enabled=$3, current_revision=$4, updated_at=now()
		WHERE key=$1`, key, input.Name, input.Enabled, next); err != nil {
		if isDuplicate(err) {
			return Template{}, ErrTemplateDuplicate
		}
		return Template{}, fmt.Errorf("entitlements: advance template revision: %w", err)
	}
	template, err = getTemplate(ctx, tx, key, next, false)
	if err != nil {
		return Template{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO entitlement_audit_events
		(action,template_key,template_revision) VALUES ('entitlement_template.revised',$1,$2)`, key, next); err != nil {
		return Template{}, fmt.Errorf("entitlements: audit template revision: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Template{}, fmt.Errorf("entitlements: commit template revision: %w", err)
	}
	return template, nil
}

// Clone creates revision 1 under a new key using an exact source revision.
func (s *Store) Clone(ctx context.Context, sourceKey string, sourceRevision int64, input CreateTemplateInput) (Template, error) {
	source, err := s.Get(ctx, sourceKey, sourceRevision)
	if err != nil {
		return Template{}, err
	}
	input.Policy = source.Policy
	return s.Create(ctx, input)
}

// Archive makes a template unavailable without deleting its history.
func (s *Store) Archive(ctx context.Context, key string, expectedRevision int64) (Template, error) {
	key = strings.TrimSpace(key)
	if key == "browse-only" {
		return Template{}, ErrProtectedTemplate
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Template{}, fmt.Errorf("entitlements: begin template archive: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE entitlement_templates
		SET archived=true, enabled=false, updated_at=now()
		WHERE key=$1 AND current_revision=$2 AND NOT archived`, key, expectedRevision)
	if err != nil {
		return Template{}, fmt.Errorf("entitlements: archive template: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var current int64
		if err := tx.QueryRow(ctx, `SELECT current_revision FROM entitlement_templates WHERE key=$1`, key).Scan(&current); errors.Is(err, pgx.ErrNoRows) {
			return Template{}, ErrTemplateNotFound
		} else if err != nil {
			return Template{}, fmt.Errorf("entitlements: inspect archive conflict: %w", err)
		}
		return Template{}, ErrRevisionConflict
	}
	item, err := getTemplate(ctx, tx, key, expectedRevision, false)
	if err != nil {
		return Template{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entitlement_audit_events
		(action,template_key,template_revision) VALUES ('entitlement_template.archived',$1,$2)`, key, expectedRevision); err != nil {
		return Template{}, fmt.Errorf("entitlements: audit template archive: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Template{}, fmt.Errorf("entitlements: commit template archive: %w", err)
	}
	return item, nil
}

// ApplyTemplate materializes an exact revision into a tenant's managed default
// group. Dry runs use the same transaction and diff path without writing.
func (s *Store) ApplyTemplate(ctx context.Context, tenantID uuid.UUID, key string, revision int64, dryRun bool) (result ApplyResult, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: begin template apply: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err = ApplyTemplateInTx(ctx, tx, tenantID, key, revision, dryRun)
	if err != nil {
		return ApplyResult{}, err
	}
	if dryRun {
		return result, nil
	}
	if err = tx.Commit(ctx); err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: commit template apply: %w", err)
	}
	return result, nil
}

// ApplyTemplateInTx is the atomic tenant-provisioning boundary. Callers own
// the transaction and must commit only after their surrounding operation is
// complete.
func ApplyTemplateInTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, key string, revision int64, dryRun bool) (ApplyResult, error) {
	if tenantID == uuid.Nil || revision <= 0 {
		return ApplyResult{}, fmt.Errorf("%w: tenant and revision are required", ErrInvalidPolicy)
	}
	var tenantIDLocked uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id FROM organizations
		WHERE id=$1 AND external_service_id IS NOT NULL
		FOR UPDATE`, tenantID).Scan(&tenantIDLocked); errors.Is(err, pgx.ErrNoRows) {
		return ApplyResult{}, ErrTenantNotFound
	} else if err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: lock tenant: %w", err)
	}
	template, err := getTemplate(ctx, tx, strings.TrimSpace(key), revision, false)
	if err != nil {
		return ApplyResult{}, err
	}
	if !template.Enabled || template.Archived {
		return ApplyResult{}, ErrTemplateUnavailable
	}
	effectivePolicy := template.Policy
	if effectivePolicy.LibraryIDs == nil {
		rows, err := tx.Query(ctx, `SELECT id FROM media_folders WHERE enabled ORDER BY id`)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("entitlements: resolve enabled libraries: %w", err)
		}
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return ApplyResult{}, fmt.Errorf("entitlements: scan enabled library: %w", err)
			}
			effectivePolicy.LibraryIDs = append(effectivePolicy.LibraryIDs, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return ApplyResult{}, fmt.Errorf("entitlements: iterate enabled libraries: %w", err)
		}
		rows.Close()
		if effectivePolicy.LibraryIDs == nil {
			effectivePolicy.LibraryIDs = []int{}
		}
	}

	group, err := loadMaterializationGroup(ctx, tx, tenantID)
	if err != nil {
		return ApplyResult{}, err
	}
	result := ApplyResult{
		TenantID: tenantID, TemplateKey: template.Key, TemplateRevision: template.Revision,
		DryRun: dryRun, Policy: effectivePolicy,
	}
	if group != nil {
		result.GroupID = group.ID
		result.PreviousTemplateKey = group.TemplateKey
		result.PreviousTemplateRevision = group.TemplateRevision
		result.Changed = !group.IsDefault || group.TemplateKey != template.Key || group.TemplateRevision != template.Revision || !policiesEqual(group.Policy, effectivePolicy)
	} else {
		result.Changed = true
	}
	if dryRun || !result.Changed {
		return result, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE access_groups SET is_default=false, updated_at=now()
		WHERE organization_id=$1 AND is_default`, tenantID); err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: clear previous tenant default: %w", err)
	}
	if group == nil {
		if err := tx.QueryRow(ctx, `
			INSERT INTO access_groups (
				organization_id, name, description, is_default, library_ids,
				max_playback_quality, playback_allowed, download_allowed,
				download_transcode_allowed, max_streams, max_profiles,
				transcode_allowed, max_transcodes, allowed_permissions,
				requests_allowed, managed_template_key, managed_template_revision
			)
			VALUES ($1, $2, 'Managed from a Vondel entitlement template.', true, $3,
			        $4, $5, $6, $7, $8, $9, $10, $11, $12,
			        $13, $14, $15)
			RETURNING id`,
			tenantID, "Managed Entitlement "+template.Key, effectivePolicy.LibraryIDs,
			effectivePolicy.MaxPlaybackQuality, effectivePolicy.PlaybackAllowed,
			effectivePolicy.DownloadAllowed, effectivePolicy.DownloadTranscodeAllowed,
			effectivePolicy.MaxStreams, effectivePolicy.MaxProfiles,
			effectivePolicy.TranscodeAllowed, effectivePolicy.MaxTranscodes,
			effectivePolicy.AllowedPermissions, effectivePolicy.RequestsAllowed,
			template.Key, template.Revision).
			Scan(&result.GroupID); err != nil {
			return ApplyResult{}, fmt.Errorf("entitlements: create managed default group: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE access_groups
			SET description='Managed from a Vondel entitlement template.',
			    is_default=true, library_ids=$3, max_playback_quality=$4,
			    playback_allowed=$5, download_allowed=$6,
			    download_transcode_allowed=$7, max_streams=$8,
			    max_profiles=$9, transcode_allowed=$10, max_transcodes=$11,
			    allowed_permissions=$12, requests_allowed=$13,
			    managed_template_key=$14, managed_template_revision=$15,
			    updated_at=now()
			WHERE organization_id=$1 AND id=$2`,
			tenantID, group.ID, effectivePolicy.LibraryIDs,
			effectivePolicy.MaxPlaybackQuality, effectivePolicy.PlaybackAllowed,
			effectivePolicy.DownloadAllowed, effectivePolicy.DownloadTranscodeAllowed,
			effectivePolicy.MaxStreams, effectivePolicy.MaxProfiles,
			effectivePolicy.TranscodeAllowed, effectivePolicy.MaxTranscodes,
			effectivePolicy.AllowedPermissions, effectivePolicy.RequestsAllowed,
			template.Key, template.Revision); err != nil {
			return ApplyResult{}, fmt.Errorf("entitlements: update managed default group: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET access_policy_revision=access_policy_revision+1
		WHERE id IN (
			SELECT DISTINCT user_id FROM user_profiles
			WHERE organization_id=$1 AND access_group_id=$2
		)`, tenantID, result.GroupID); err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: bump managed group member revisions: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entitlement_audit_events
		(action,organization_id,template_key,template_revision)
		VALUES ('organization.entitlement_materialized',$1,$2,$3)`, tenantID, template.Key, template.Revision); err != nil {
		return ApplyResult{}, fmt.Errorf("entitlements: audit managed group materialization: %w", err)
	}
	return result, nil
}

func resolveMaterializedPolicy(ctx context.Context, tx pgx.Tx, policy Policy) (Policy, error) {
	if policy.LibraryIDs != nil {
		return policy, nil
	}
	rows, err := tx.Query(ctx, `SELECT id FROM media_folders WHERE enabled ORDER BY id`)
	if err != nil {
		return Policy{}, fmt.Errorf("entitlements: resolve enabled libraries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return Policy{}, fmt.Errorf("entitlements: scan enabled library: %w", err)
		}
		policy.LibraryIDs = append(policy.LibraryIDs, id)
	}
	if err := rows.Err(); err != nil {
		return Policy{}, fmt.Errorf("entitlements: iterate enabled libraries: %w", err)
	}
	if policy.LibraryIDs == nil {
		policy.LibraryIDs = []int{}
	}
	return policy, nil
}

type templateQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getTemplate(ctx context.Context, db templateQuerier, key string, revision int64, latest bool) (Template, error) {
	query := `
		SELECT t.key, t.name, r.revision, t.enabled, t.archived,
		       r.library_ids, r.playback_allowed, r.max_streams, r.max_profiles,
		       r.transcode_allowed, r.max_transcodes, r.download_allowed,
		       r.download_transcode_allowed, r.max_playback_quality,
		       r.allowed_permissions, r.requests_allowed, r.created_at
		FROM entitlement_templates t
		JOIN entitlement_template_revisions r ON r.template_key=t.key
		WHERE t.key=$1 AND r.revision=`
	args := []any{key}
	if latest {
		query += `t.current_revision`
	} else {
		query += `$2`
		args = append(args, revision)
	}
	template, err := scanTemplate(db.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrTemplateNotFound
	}
	if err != nil {
		return Template{}, fmt.Errorf("entitlements: load template: %w", err)
	}
	return template, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanTemplate(row rowScanner) (Template, error) {
	var template Template
	err := row.Scan(
		&template.Key, &template.Name, &template.Revision, &template.Enabled, &template.Archived,
		&template.Policy.LibraryIDs, &template.Policy.PlaybackAllowed, &template.Policy.MaxStreams,
		&template.Policy.MaxProfiles, &template.Policy.TranscodeAllowed, &template.Policy.MaxTranscodes,
		&template.Policy.DownloadAllowed, &template.Policy.DownloadTranscodeAllowed,
		&template.Policy.MaxPlaybackQuality, &template.Policy.AllowedPermissions,
		&template.Policy.RequestsAllowed, &template.CreatedAt,
	)
	return template, err
}

func insertRevision(ctx context.Context, tx pgx.Tx, key string, revision int64, policy Policy) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO entitlement_template_revisions (
			template_key, revision, library_ids, playback_allowed, max_streams,
			max_profiles, transcode_allowed, max_transcodes, download_allowed,
			download_transcode_allowed, max_playback_quality, allowed_permissions,
			requests_allowed
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		key, revision, policy.LibraryIDs, policy.PlaybackAllowed, policy.MaxStreams,
		policy.MaxProfiles, policy.TranscodeAllowed, policy.MaxTranscodes,
		policy.DownloadAllowed, policy.DownloadTranscodeAllowed,
		policy.MaxPlaybackQuality, policy.AllowedPermissions, policy.RequestsAllowed)
	if err != nil {
		return fmt.Errorf("entitlements: insert template revision: %w", err)
	}
	return nil
}

type materializationGroup struct {
	ID               int64
	IsDefault        bool
	TemplateKey      string
	TemplateRevision int64
	Policy           Policy
}

func loadMaterializationGroup(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (*materializationGroup, error) {
	var (
		group       materializationGroup
		templateKey *string
		revision    *int64
	)
	err := tx.QueryRow(ctx, `
		SELECT id, is_default, managed_template_key, managed_template_revision,
		       library_ids, playback_allowed, max_streams, max_profiles,
		       transcode_allowed, max_transcodes, download_allowed,
		       download_transcode_allowed, max_playback_quality,
		       allowed_permissions, requests_allowed
		FROM access_groups
		WHERE organization_id=$1
		  AND (
			(is_default AND managed_template_key IS NOT NULL) OR
			(is_default AND name='Default Group' AND description='Applied automatically to newly created users.')
		  )
		ORDER BY (managed_template_key IS NOT NULL) DESC, id
		LIMIT 1
		FOR UPDATE`, tenantID).Scan(
		&group.ID, &group.IsDefault, &templateKey, &revision,
		&group.Policy.LibraryIDs, &group.Policy.PlaybackAllowed, &group.Policy.MaxStreams,
		&group.Policy.MaxProfiles, &group.Policy.TranscodeAllowed, &group.Policy.MaxTranscodes,
		&group.Policy.DownloadAllowed, &group.Policy.DownloadTranscodeAllowed,
		&group.Policy.MaxPlaybackQuality, &group.Policy.AllowedPermissions,
		&group.Policy.RequestsAllowed,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("entitlements: load managed default group: %w", err)
	}
	if templateKey != nil {
		group.TemplateKey = *templateKey
		group.TemplateRevision = *revision
	}
	return &group, nil
}

func policiesEqual(left, right Policy) bool {
	left.MaxPlaybackQuality = normalizePlaybackQuality(left.MaxPlaybackQuality)
	right.MaxPlaybackQuality = normalizePlaybackQuality(right.MaxPlaybackQuality)
	return reflect.DeepEqual(left, right)
}

func normalizePlaybackQuality(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "", "ANY":
		return ""
	case "STANDARD", "480P", "720P", "1080P":
		return "1080p"
	case "4K", "UHD", "2160P", "4320P":
		return "2160p"
	default:
		return ""
	}
}

func isDuplicate(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
