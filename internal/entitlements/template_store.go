// Package entitlements owns revisioned per-member policy templates and their
// materialization into tenant-managed default access groups.
package entitlements

import (
	"context"
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
	ErrRevisionConflict = errors.New("entitlements: template revision conflict")
	// ErrInvalidPolicy reports a locally invalid template policy.
	ErrInvalidPolicy = errors.New("entitlements: invalid policy")
	// ErrTenantNotFound reports an unknown or non-Park tenant organization.
	ErrTenantNotFound = errors.New("entitlements: tenant not found")
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
	TemplateKey              string
	TemplateRevision         int64
	GroupID                  int64
	DryRun                   bool
	Changed                  bool
	PreviousTemplateKey      string
	PreviousTemplateRevision int64
	Policy                   Policy
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
	tag, err := s.pool.Exec(ctx, `
		UPDATE entitlement_templates
		SET archived=true, enabled=false, updated_at=now()
		WHERE key=$1 AND current_revision=$2 AND NOT archived`, key, expectedRevision)
	if err != nil {
		return Template{}, fmt.Errorf("entitlements: archive template: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var current int64
		if err := s.pool.QueryRow(ctx, `SELECT current_revision FROM entitlement_templates WHERE key=$1`, key).Scan(&current); errors.Is(err, pgx.ErrNoRows) {
			return Template{}, ErrTemplateNotFound
		} else if err != nil {
			return Template{}, fmt.Errorf("entitlements: inspect archive conflict: %w", err)
		}
		return Template{}, ErrRevisionConflict
	}
	return s.Latest(ctx, key)
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
	return result, nil
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
			managed_template_key IS NOT NULL OR
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
