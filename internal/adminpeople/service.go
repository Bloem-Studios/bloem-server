// Package adminpeople owns organization-scoped people administration,
// immutable selections, and durable bulk results.
package adminpeople

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/idgen"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultLimit = 50
	maximumLimit = 200
	selectionTTL = 15 * time.Minute

	SortName           = "name"
	SortEmail          = "email"
	SortRecentActivity = "recent_activity"

	BulkAssignGroup           = "assign_group"
	BulkSuspendMemberships    = "suspend_memberships"
	BulkReactivateMemberships = "reactivate_memberships"

	ReasonAlreadyApplied       = "already_applied"
	ReasonNoProfiles           = "no_profiles"
	ReasonNotFound             = "not_found"
	ReasonProtectedOwner       = "protected_owner"
	ReasonMutationFailed       = "mutation_failed"
	ReasonAuthorizationChanged = "authorization_state_changed"
	ReasonActorAuthorityTarget = "actor_authority_target"

	AuthorityPlatformAdmin     = "platform_admin"
	AuthorityOrganizationAdmin = "organization_admin"
	maximumSelectionTargets    = 10000
	defaultBulkBatchSize       = 100
	bulkStatusCompleted        = "completed"
	bulkStatusFailed           = "failed"
	bulkStatusCancelled        = "cancelled" //nolint:misspell // Durable API/DB spelling.
	auditStatusField           = "status"
)

var (
	ErrNotFound                  = errors.New("organization person resource not found")
	ErrInvalidCursor             = errors.New("invalid people cursor")
	ErrInvalidSelection          = errors.New("invalid immutable selection")
	ErrSelectionExpired          = errors.New("immutable selection expired")
	ErrInvalidFilter             = errors.New("invalid people filter")
	ErrInvalidBulkAction         = errors.New("invalid people bulk action")
	ErrAuthorizationStateChanged = errors.New("authorization state changed")
	ErrBulkIdempotencyConflict   = errors.New("people bulk idempotency conflict")
	ErrBulkJobNotCancellable     = errors.New("people bulk job is not cancellable through this endpoint")
)

type Filter struct {
	Query    string
	Status   []tenancy.MembershipStatus
	GroupIDs []int
	// AccountIDs is an explicit Server account selector. It is canonicalized
	// and bounded with the rest of the immutable selection input.
	AccountIDs []int
	// RequireAllAccountIDs makes an explicit selector fail closed when any
	// account is absent from the requested organization scope.
	RequireAllAccountIDs bool
	ActiveSince          *time.Time
	Sort                 string
	Limit                int
	Cursor               string
}

type ProfileSummary struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	GroupID   int       `json:"group_id"`
	GroupName string    `json:"group_name"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PersonSummary struct {
	OrganizationID   uuid.UUID                `json:"organization_id"`
	AccountID        int                      `json:"account_id"`
	Email            string                   `json:"email"`
	DisplayName      string                   `json:"display_name"`
	MembershipID     uuid.UUID                `json:"membership_id"`
	MembershipStatus tenancy.MembershipStatus `json:"membership_status"`
	LegacyRole       string                   `json:"legacy_role"`
	SecurityRevision int64                    `json:"security_revision"`
	LastActivity     time.Time                `json:"last_activity"`
	Profiles         []ProfileSummary         `json:"profiles"`
}

type Page struct {
	Items            []PersonSummary `json:"items"`
	NextCursor       string          `json:"next_cursor,omitempty"`
	ApproximateTotal int64           `json:"approximate_total"`
}

type Selection struct {
	Token     string    `json:"token"`
	Matched   int64     `json:"matched"`
	Excluded  int64     `json:"excluded"`
	ExpiresAt time.Time `json:"expires_at"`
}

type BulkAction struct {
	SelectionToken string `json:"selection_token"`
	Kind           string `json:"kind"`
	GroupID        *int   `json:"group_id,omitempty"`
}

type PolicyBulkAction struct {
	SelectionToken    string        `json:"selection_token"`
	ConfirmationToken string        `json:"confirmation_token"`
	IdempotencyKey    string        `json:"idempotency_key"`
	Command           PolicyCommand `json:"command"`
}

type RecordResult struct {
	AccountID int    `json:"account_id"`
	Reason    string `json:"reason"`
}

type BulkResult struct {
	JobID                string         `json:"job_id"`
	Status               string         `json:"status"`
	ProgressCurrent      int            `json:"progress_current"`
	ProgressTotal        int            `json:"progress_total"`
	Succeeded            int            `json:"succeeded"`
	Skipped              []RecordResult `json:"skipped"`
	Failed               []RecordResult `json:"failed"`
	TargetCohortID       uuid.UUID      `json:"target_cohort_id,omitempty"`
	TargetCohortRevision int64          `json:"target_cohort_revision,omitempty"`
	TargetGroupID        int64          `json:"target_group_id,omitempty"`
}

type MutationActor struct {
	AccountID        int
	Authority        string
	MembershipID     uuid.UUID
	SecurityRevision int64
	PolicyRevision   int64
	RequestID        string
}
type mutationMetadataKey struct{}

func WithMutationRequestID(ctx context.Context, requestID string) context.Context {
	actor, _ := MutationActorFromContext(ctx)
	actor.RequestID = strings.TrimSpace(requestID)
	return WithMutationActor(ctx, actor)
}

func WithMutationActor(ctx context.Context, actor MutationActor) context.Context {
	return context.WithValue(ctx, mutationMetadataKey{}, actor)
}
func MutationActorFromContext(ctx context.Context) (MutationActor, bool) {
	actor, ok := ctx.Value(mutationMetadataKey{}).(MutationActor)
	return actor, ok
}

func mutationActor(ctx context.Context, accountID int) (MutationActor, error) {
	actor, ok := MutationActorFromContext(ctx)
	if !ok {
		actor = MutationActor{AccountID: accountID, Authority: AuthorityOrganizationAdmin}
	}
	if actor.AccountID == 0 {
		actor.AccountID = accountID
	}
	if actor.AccountID != accountID || (actor.Authority != AuthorityOrganizationAdmin && actor.Authority != AuthorityPlatformAdmin) {
		return MutationActor{}, ErrInvalidBulkAction
	}
	return actor, nil
}

type DurableJob struct {
	JobID          string
	OrganizationID uuid.UUID
}

type Service struct {
	pool                  *pgxpool.Pool
	key                   [32]byte
	now                   func() time.Time
	selectionSnapshotHook func()
}

func NewService(pool *pgxpool.Pool, secret string) *Service {
	return &Service{pool: pool, key: sha256.Sum256([]byte(secret)), now: time.Now}
}

type canonicalFilter struct {
	Query       string                     `json:"query,omitempty"`
	Status      []tenancy.MembershipStatus `json:"status,omitempty"`
	GroupIDs    []int                      `json:"group_ids,omitempty"`
	AccountIDs  []int                      `json:"account_ids,omitempty"`
	ActiveSince *time.Time                 `json:"active_since,omitempty"`
	Sort        string                     `json:"sort"`
}

func canonicalizeSelectionFilter(filter Filter) canonicalFilter {
	value := canonicalFilter{Query: strings.ToLower(strings.TrimSpace(filter.Query)), Sort: canonicalSort(filter.Sort)}
	statusSet := make(map[tenancy.MembershipStatus]struct{}, len(filter.Status))
	for _, status := range filter.Status {
		statusSet[status] = struct{}{}
	}
	for status := range statusSet {
		value.Status = append(value.Status, status)
	}
	sort.Slice(value.Status, func(i, j int) bool { return value.Status[i] < value.Status[j] })
	groupSet := make(map[int]struct{}, len(filter.GroupIDs))
	for _, id := range filter.GroupIDs {
		if id > 0 {
			groupSet[id] = struct{}{}
		}
	}
	for id := range groupSet {
		value.GroupIDs = append(value.GroupIDs, id)
	}
	sort.Ints(value.GroupIDs)
	accountSet := make(map[int]struct{}, len(filter.AccountIDs))
	for _, id := range filter.AccountIDs {
		if id > 0 {
			accountSet[id] = struct{}{}
		}
	}
	for id := range accountSet {
		value.AccountIDs = append(value.AccountIDs, id)
	}
	sort.Ints(value.AccountIDs)
	if filter.ActiveSince != nil {
		active := filter.ActiveSince.UTC()
		value.ActiveSince = &active
	}
	return value
}

func (f canonicalFilter) toFilter() Filter {
	return Filter{Query: f.Query, Status: f.Status, GroupIDs: f.GroupIDs, AccountIDs: f.AccountIDs, ActiveSince: f.ActiveSince, Sort: f.Sort}
}

func canonicalSort(value string) string {
	switch strings.TrimSpace(value) {
	case "", SortName:
		return SortName
	case SortEmail:
		return SortEmail
	case SortRecentActivity:
		return SortRecentActivity
	default:
		return ""
	}
}

func validateFilter(filter Filter) error {
	if canonicalSort(filter.Sort) == "" {
		return ErrInvalidFilter
	}
	for _, status := range filter.Status {
		if status != tenancy.MembershipInvited && status != tenancy.MembershipActive && status != tenancy.MembershipSuspended {
			return ErrInvalidFilter
		}
	}
	for _, id := range filter.GroupIDs {
		if id <= 0 {
			return ErrInvalidFilter
		}
	}
	if len(filter.AccountIDs) > maximumSelectionTargets || (filter.RequireAllAccountIDs && len(filter.AccountIDs) == 0) {
		return ErrInvalidFilter
	}
	for _, id := range filter.AccountIDs {
		if id <= 0 {
			return ErrInvalidFilter
		}
	}
	if filter.Limit < 0 || filter.Limit > maximumLimit {
		return ErrInvalidFilter
	}
	return nil
}

type peopleCursor struct {
	OrganizationID uuid.UUID       `json:"organization_id"`
	Sort           string          `json:"sort"`
	Filter         canonicalFilter `json:"filter"`
	Key            string          `json:"key,omitempty"`
	Activity       time.Time       `json:"activity,omitempty"`
	AccountID      int             `json:"account_id"`
}

func (s *Service) List(ctx context.Context, organizationID uuid.UUID, filter Filter) (Page, error) {
	return s.list(ctx, organizationID, filter, nil)
}

func (s *Service) Get(ctx context.Context, organizationID uuid.UUID, accountID int) (PersonSummary, error) {
	if accountID <= 0 {
		return PersonSummary{}, ErrNotFound
	}
	page, err := s.list(ctx, organizationID, Filter{Limit: 1}, &accountID)
	if err != nil {
		return PersonSummary{}, err
	}
	if len(page.Items) != 1 {
		return PersonSummary{}, ErrNotFound
	}
	return page.Items[0], nil
}

func (s *Service) list(ctx context.Context, organizationID uuid.UUID, filter Filter, accountID *int) (Page, error) {
	if s == nil || s.pool == nil {
		return Page{}, fmt.Errorf("people store unavailable")
	}
	if organizationID == uuid.Nil || validateFilter(filter) != nil {
		return Page{}, ErrInvalidFilter
	}
	filter.Sort = canonicalSort(filter.Sort)
	canonical := canonicalizeSelectionFilter(filter)
	limit := filter.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	conditions, args := buildPeopleConditions(organizationID, filter, nil)
	if accountID != nil {
		args = append(args, *accountID)
		conditions = append(conditions, fmt.Sprintf("m.account_id = $%d", len(args)))
	}
	activityExpr := `GREATEST(m.updated_at, COALESCE((SELECT max(p.updated_at) FROM user_profiles p WHERE p.organization_id=m.organization_id AND p.user_id=m.account_id), m.updated_at))`
	nameExpr := `lower(COALESCE(NULLIF(u.username,''),u.email,''))`
	emailExpr := `lower(COALESCE(u.email,''))`
	if filter.Cursor != "" {
		cursor, err := s.decodePeopleCursor(filter.Cursor)
		cursorFilter, _ := json.Marshal(cursor.Filter)
		expectedFilter, _ := json.Marshal(canonical)
		if err != nil || cursor.OrganizationID != organizationID || cursor.Sort != filter.Sort || cursor.AccountID <= 0 || !hmac.Equal(cursorFilter, expectedFilter) {
			return Page{}, ErrInvalidCursor
		}
		switch filter.Sort {
		case SortName:
			args = append(args, cursor.Key, cursor.AccountID)
			conditions = append(conditions, fmt.Sprintf("(%s,m.account_id) > ($%d,$%d)", nameExpr, len(args)-1, len(args)))
		case SortEmail:
			args = append(args, cursor.Key, cursor.AccountID)
			conditions = append(conditions, fmt.Sprintf("(%s,m.account_id) > ($%d,$%d)", emailExpr, len(args)-1, len(args)))
		case SortRecentActivity:
			if cursor.Activity.IsZero() {
				return Page{}, ErrInvalidCursor
			}
			args = append(args, cursor.Activity, cursor.AccountID)
			conditions = append(conditions, fmt.Sprintf("(%s,m.account_id) < ($%d,$%d)", activityExpr, len(args)-1, len(args)))
		}
	}
	order := nameExpr + `,m.account_id`
	if filter.Sort == SortEmail {
		order = emailExpr + `,m.account_id`
	}
	if filter.Sort == SortRecentActivity {
		order = activityExpr + ` DESC,m.account_id DESC`
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, `
		SELECT m.organization_id,m.account_id,COALESCE(u.email,''),COALESCE(NULLIF(u.username,''),u.email,''),
		       m.id,m.status,m.legacy_role,m.security_revision,`+activityExpr+`,`+nameExpr+`,`+emailExpr+`
		FROM organization_memberships m JOIN users u ON u.id=m.account_id
		WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY `+order+` LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return Page{}, fmt.Errorf("list organization people: %w", err)
	}
	defer rows.Close()
	type rowValue struct {
		person            PersonSummary
		nameKey, emailKey string
	}
	values := make([]rowValue, 0, limit+1)
	for rows.Next() {
		var value rowValue
		if err := rows.Scan(&value.person.OrganizationID, &value.person.AccountID, &value.person.Email, &value.person.DisplayName, &value.person.MembershipID, &value.person.MembershipStatus, &value.person.LegacyRole, &value.person.SecurityRevision, &value.person.LastActivity, &value.nameKey, &value.emailKey); err != nil {
			return Page{}, fmt.Errorf("scan organization person: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("iterate organization people: %w", err)
	}
	page := Page{Items: make([]PersonSummary, 0, min(len(values), limit))}
	visible := values
	if len(visible) > limit {
		visible = visible[:limit]
	}
	for _, value := range visible {
		page.Items = append(page.Items, value.person)
	}
	if err := s.loadProfiles(ctx, organizationID, page.Items); err != nil {
		return Page{}, err
	}
	countConditions, countArgs := buildPeopleConditions(organizationID, filter, nil)
	if accountID != nil {
		countArgs = append(countArgs, *accountID)
		countConditions = append(countConditions, fmt.Sprintf("m.account_id=$%d", len(countArgs)))
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE `+strings.Join(countConditions, " AND "), countArgs...).Scan(&page.ApproximateTotal); err != nil {
		return Page{}, fmt.Errorf("count organization people: %w", err)
	}
	if len(values) > limit {
		last := values[limit-1]
		cursor := peopleCursor{OrganizationID: organizationID, Sort: filter.Sort, Filter: canonical, AccountID: last.person.AccountID}
		switch filter.Sort {
		case SortName:
			cursor.Key = last.nameKey
		case SortEmail:
			cursor.Key = last.emailKey
		case SortRecentActivity:
			cursor.Activity = last.person.LastActivity
		}
		page.NextCursor = s.encodePeopleCursor(cursor)
	}
	return page, nil
}

func buildPeopleConditions(organizationID uuid.UUID, filter Filter, snapshot *time.Time) ([]string, []any) {
	conditions := []string{"m.organization_id=$1"}
	args := []any{organizationID}
	profileSnapshot := ""
	if snapshot != nil {
		args = append(args, *snapshot)
		conditions = append(conditions, fmt.Sprintf("m.created_at <= $%d", len(args)))
		profileSnapshot = fmt.Sprintf(" AND p.created_at <= $%d", len(args))
	}
	if query := strings.ToLower(strings.TrimSpace(filter.Query)); query != "" {
		args = append(args, "%"+query+"%")
		n := len(args)
		conditions = append(conditions, fmt.Sprintf("(lower(COALESCE(u.email,'')) LIKE $%d OR lower(COALESCE(u.username,'')) LIKE $%d OR EXISTS (SELECT 1 FROM user_profiles p WHERE p.organization_id=m.organization_id AND p.user_id=m.account_id%s AND lower(p.name) LIKE $%d))", n, n, profileSnapshot, n))
	}
	if len(filter.Status) > 0 {
		values := make([]string, 0, len(filter.Status))
		for _, status := range filter.Status {
			values = append(values, string(status))
		}
		args = append(args, values)
		conditions = append(conditions, fmt.Sprintf("m.status=ANY($%d::text[])", len(args)))
	}
	if len(filter.GroupIDs) > 0 {
		values := make([]int64, 0, len(filter.GroupIDs))
		for _, id := range filter.GroupIDs {
			values = append(values, int64(id))
		}
		args = append(args, values)
		conditions = append(conditions, fmt.Sprintf("EXISTS (SELECT 1 FROM user_profiles p WHERE p.organization_id=m.organization_id AND p.user_id=m.account_id%s AND p.access_group_id=ANY($%d::bigint[]))", profileSnapshot, len(args)))
	}
	if len(filter.AccountIDs) > 0 {
		args = append(args, filter.AccountIDs)
		conditions = append(conditions, fmt.Sprintf("m.account_id=ANY($%d::integer[])", len(args)))
	}
	if filter.ActiveSince != nil {
		args = append(args, filter.ActiveSince.UTC())
		n := len(args)
		conditions = append(conditions, fmt.Sprintf("(m.updated_at >= $%d OR EXISTS (SELECT 1 FROM user_profiles p WHERE p.organization_id=m.organization_id AND p.user_id=m.account_id%s AND p.updated_at >= $%d))", n, profileSnapshot, n))
	}
	return conditions, args
}

func (s *Service) loadProfiles(ctx context.Context, organizationID uuid.UUID, people []PersonSummary) error {
	if len(people) == 0 {
		return nil
	}
	ids := make([]int, 0, len(people))
	indexes := make(map[int]int, len(people))
	for i := range people {
		ids = append(ids, people[i].AccountID)
		indexes[people[i].AccountID] = i
		people[i].Profiles = []ProfileSummary{}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.user_id,p.id,p.name,p.access_group_id,g.name,p.updated_at
		FROM user_profiles p JOIN access_groups g ON g.organization_id=p.organization_id AND g.id=p.access_group_id
		WHERE p.organization_id=$1 AND p.user_id=ANY($2::int[]) ORDER BY p.user_id,lower(p.name),p.id`, organizationID, ids)
	if err != nil {
		return fmt.Errorf("list organization profiles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var accountID int
		var profile ProfileSummary
		if err := rows.Scan(&accountID, &profile.ID, &profile.Name, &profile.GroupID, &profile.GroupName, &profile.UpdatedAt); err != nil {
			return fmt.Errorf("scan organization profile: %w", err)
		}
		i := indexes[accountID]
		people[i].Profiles = append(people[i].Profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate organization profiles: %w", err)
	}
	return nil
}

func (s *Service) encodePeopleCursor(cursor peopleCursor) string {
	raw, _ := json.Marshal(cursor)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write([]byte("cursor." + payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Service) decodePeopleCursor(value string) (peopleCursor, error) {
	var cursor peopleCursor
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return cursor, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || base64.RawURLEncoding.EncodeToString(signature) != parts[1] {
		return cursor, ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write([]byte("cursor." + parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursor, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != parts[0] || json.Unmarshal(raw, &cursor) != nil {
		return peopleCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

type profileSnapshot struct {
	ID              string    `json:"id"`
	GroupID         int       `json:"group_id"`
	InheritsAccount bool      `json:"inherits_account"`
	UpdatedAt       time.Time `json:"updated_at"`
}
type targetSnapshot struct {
	AccountID              int                      `json:"account_id"`
	AccountIncarnationID   uuid.UUID                `json:"account_incarnation_id"`
	MembershipID           uuid.UUID                `json:"membership_id"`
	MembershipStatus       tenancy.MembershipStatus `json:"membership_status"`
	MembershipRevision     int64                    `json:"membership_revision"`
	AccountPolicyRevision  int64                    `json:"account_policy_revision"`
	GroupID                int64                    `json:"group_id"`
	CohortID               uuid.UUID                `json:"cohort_id,omitempty"`
	CohortRevision         int64                    `json:"cohort_revision,omitempty"`
	SourceTemplateKey      string                   `json:"source_template_key,omitempty"`
	SourceTemplateRevision int64                    `json:"source_template_revision,omitempty"`
	Profiles               []profileSnapshot        `json:"profiles"`
}

func (s *Service) CreateSelection(ctx context.Context, organizationID uuid.UUID, filter Filter) (Selection, error) {
	if s == nil || s.pool == nil {
		return Selection{}, fmt.Errorf("people store unavailable")
	}
	if organizationID == uuid.Nil || validateFilter(filter) != nil {
		return Selection{}, ErrInvalidFilter
	}
	canonical := canonicalizeSelectionFilter(filter)
	snapshot := s.now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Selection{}, fmt.Errorf("begin people selection snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The materialized account IDs are the immutable snapshot. snapshot_at is
	// retained for confirmation/audit; adding an application-clock cutoff here
	// would incorrectly exclude rows when database and application clocks skew.
	conditions, args := buildPeopleConditions(organizationID, canonical.toFilter(), nil)
	rows, err := tx.Query(ctx, `
		SELECT m.account_id,u.account_incarnation_id,m.id,m.status,m.security_revision,m.access_policy_revision,
		       COALESCE(m.access_group_id,0),COALESCE(g.managed_cohort_id,'00000000-0000-0000-0000-000000000000'::uuid),COALESCE(r.revision,0),
		       COALESCE(r.source_template_key,g.managed_template_key,''),COALESCE(r.source_template_revision,g.managed_template_revision,0),
		       COALESCE((SELECT jsonb_agg(jsonb_build_object('id',p.id,'group_id',p.access_group_id,'inherits_account',p.access_group_id IS NOT DISTINCT FROM m.access_group_id,'updated_at',p.updated_at) ORDER BY p.id) FROM user_profiles p WHERE p.organization_id=m.organization_id AND p.user_id=m.account_id),'[]'::jsonb)
		FROM organization_memberships m
		JOIN users u ON u.id=m.account_id
		LEFT JOIN access_groups g ON g.organization_id=m.organization_id AND g.id=m.access_group_id
		LEFT JOIN entitlement_policy_cohort_revisions r ON r.organization_id=g.organization_id AND r.id=g.managed_cohort_id AND r.access_group_id=g.id
		WHERE `+strings.Join(conditions, " AND ")+` ORDER BY m.account_id LIMIT `+strconv.Itoa(maximumSelectionTargets+1), args...)
	if err != nil {
		return Selection{}, fmt.Errorf("snapshot people selection: %w", err)
	}
	ids := []int{}
	targets := []targetSnapshot{}
	for rows.Next() {
		var target targetSnapshot
		var profilesJSON []byte
		if err := rows.Scan(&target.AccountID, &target.AccountIncarnationID, &target.MembershipID, &target.MembershipStatus, &target.MembershipRevision, &target.AccountPolicyRevision, &target.GroupID, &target.CohortID, &target.CohortRevision, &target.SourceTemplateKey, &target.SourceTemplateRevision, &profilesJSON); err != nil {
			rows.Close()
			return Selection{}, fmt.Errorf("scan people selection: %w", err)
		}
		if err := json.Unmarshal(profilesJSON, &target.Profiles); err != nil {
			rows.Close()
			return Selection{}, fmt.Errorf("decode people selection profiles: %w", err)
		}
		if len(targets) < maximumSelectionTargets {
			ids = append(ids, target.AccountID)
			targets = append(targets, target)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Selection{}, fmt.Errorf("iterate people selection: %w", err)
	}
	rows.Close()
	if s.selectionSnapshotHook != nil {
		s.selectionSnapshotHook()
	}
	var total int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE `+strings.Join(conditions, " AND "), args...).Scan(&total); err != nil {
		return Selection{}, fmt.Errorf("count people selection: %w", err)
	}
	if filter.RequireAllAccountIDs && total != int64(len(canonical.AccountIDs)) {
		return Selection{}, ErrNotFound
	}
	reference := uuid.New()
	expires := snapshot.Add(selectionTTL)
	filterJSON, err := json.Marshal(canonical)
	if err != nil {
		return Selection{}, err
	}
	targetsJSON, err := json.Marshal(targets)
	if err != nil {
		return Selection{}, err
	}
	excluded := total - int64(len(targets))
	if _, err = tx.Exec(ctx, `INSERT INTO admin_people_selections (id,organization_id,canonical_filter,snapshot_at,account_ids,matched_count,excluded_count,expires_at,targets) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, reference, organizationID, filterJSON, snapshot, ids, len(ids), excluded, expires, targetsJSON); err != nil {
		return Selection{}, fmt.Errorf("persist immutable people selection: %w", err)
	}
	token, err := s.signSelectionReference(reference)
	if err != nil {
		return Selection{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Selection{}, fmt.Errorf("commit people selection snapshot: %w", err)
	}
	return Selection{Token: token, Matched: int64(len(ids)), Excluded: excluded, ExpiresAt: expires}, nil
}

// ResolveLifecycleSelectionTargets returns the immutable account incarnations
// and memberships captured by a signed selection. It performs no live account
// lookup, preserving receipt-first replay and exact target-set binding.
func (s *Service) ResolveLifecycleSelectionTargets(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, token string) ([]lifecycleidempotency.TargetBinding, error) {
	reference, err := s.parseSelectionReference(token)
	if err != nil {
		return nil, err
	}
	var storedOrganization uuid.UUID
	var payload []byte
	query := `SELECT organization_id,targets FROM admin_people_selections WHERE id=$1`
	args := []any{reference}
	if organizationID != uuid.Nil {
		query += ` AND organization_id=$2`
		args = append(args, organizationID)
	}
	if err := tx.QueryRow(ctx, query, args...).Scan(&storedOrganization, &payload); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidSelection
	} else if err != nil {
		return nil, err
	}
	var snapshots []targetSnapshot
	if err := json.Unmarshal(payload, &snapshots); err != nil {
		return nil, ErrInvalidSelection
	}
	targets := make([]lifecycleidempotency.TargetBinding, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.AccountID <= 0 || snapshot.AccountIncarnationID == uuid.Nil || snapshot.MembershipID == uuid.Nil {
			return nil, lifecycleidempotency.ErrInvalidBinding
		}
		targets = append(targets, lifecycleidempotency.TargetBinding{OrganizationID: storedOrganization, MembershipID: snapshot.MembershipID, AccountID: snapshot.AccountID, AccountIncarnationID: snapshot.AccountIncarnationID, ResourceID: reference.String()})
	}
	if len(targets) == 0 {
		return nil, lifecycleidempotency.ErrTargetNotFound
	}
	return targets, nil
}

func (s *Service) signSelectionReference(reference uuid.UUID) (string, error) {
	if s == nil || reference == uuid.Nil {
		return "", ErrInvalidSelection
	}
	payload := base64.RawURLEncoding.EncodeToString(reference[:])
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + signature, nil
}

func (s *Service) parseSelectionReference(token string) (uuid.UUID, error) {
	parts := strings.Split(token, ".")
	if s == nil || len(parts) != 2 {
		return uuid.Nil, ErrInvalidSelection
	}
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write([]byte(parts[0]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || base64.RawURLEncoding.EncodeToString(signature) != parts[1] || !hmac.Equal(signature, mac.Sum(nil)) {
		return uuid.Nil, ErrInvalidSelection
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || base64.RawURLEncoding.EncodeToString(payload) != parts[0] || len(payload) != 16 {
		return uuid.Nil, ErrInvalidSelection
	}
	reference, err := uuid.FromBytes(payload)
	if err != nil || reference == uuid.Nil {
		return uuid.Nil, ErrInvalidSelection
	}
	return reference, nil
}

type selectionRecord struct {
	id                uuid.UUID
	organizationID    uuid.UUID
	filter            canonicalFilter
	snapshot, expires time.Time
	accountIDs        []int
	targets           []targetSnapshot
	matched, excluded int64
}

func loadSelection(ctx context.Context, row pgx.Row, organizationID uuid.UUID) (selectionRecord, error) {
	var record selectionRecord
	var filterJSON, targetsJSON []byte
	err := row.Scan(&record.id, &record.organizationID, &filterJSON, &record.snapshot, &record.expires, &record.accountIDs, &record.matched, &record.excluded, &targetsJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return selectionRecord{}, ErrInvalidSelection
	}
	if err != nil {
		return selectionRecord{}, fmt.Errorf("load immutable people selection: %w", err)
	}
	if record.organizationID != organizationID || json.Unmarshal(filterJSON, &record.filter) != nil || json.Unmarshal(targetsJSON, &record.targets) != nil {
		return selectionRecord{}, ErrInvalidSelection
	}
	return record, nil
}

func (s *Service) CleanupExpiredSelections(ctx context.Context, limit int) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("people store unavailable")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM admin_people_selections WHERE id IN (SELECT id FROM admin_people_selections WHERE expires_at<=now() ORDER BY expires_at LIMIT $1)`, limit)
	if err != nil {
		return 0, fmt.Errorf("cleanup expired people selections: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *Service) CleanupTerminalBulkJobs(ctx context.Context, completedBefore time.Time, limit int) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("people store unavailable")
	}
	if completedBefore.IsZero() {
		return 0, ErrInvalidFilter
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	tag, err := s.pool.Exec(ctx, `
		WITH doomed AS (
			SELECT j.id
			FROM admin_jobs j
			JOIN admin_people_bulk_jobs b ON b.job_id=j.id
			WHERE j.status IN ('completed','failed','cancelled') AND j.completed_at <= $1
			ORDER BY j.completed_at,j.id
			LIMIT $2
		)
		DELETE FROM admin_jobs j USING doomed WHERE j.id=doomed.id`, completedBefore.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("cleanup terminal people bulk jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *Service) ListRunnableBulkJobs(ctx context.Context, limit int) ([]DurableJob, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("people store unavailable")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT b.job_id,b.organization_id FROM admin_people_bulk_jobs b JOIN admin_jobs j ON j.id=b.job_id WHERE j.status IN ('queued','running') AND b.cancel_requested_at IS NULL AND b.next_attempt_at<=now() ORDER BY j.requested_at,b.job_id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list runnable people jobs: %w", err)
	}
	defer rows.Close()
	jobs := []DurableJob{}
	for rows.Next() {
		var job DurableJob
		if err := rows.Scan(&job.JobID, &job.OrganizationID); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func validateBulkAction(action BulkAction) error {
	switch action.Kind {
	case BulkAssignGroup:
		if action.GroupID == nil || *action.GroupID <= 0 {
			return ErrInvalidBulkAction
		}
	case BulkSuspendMemberships, BulkReactivateMemberships:
		if action.GroupID != nil {
			return ErrInvalidBulkAction
		}
	default:
		return ErrInvalidBulkAction
	}
	if strings.TrimSpace(action.SelectionToken) == "" {
		return ErrInvalidBulkAction
	}
	return nil
}

func (s *Service) ExecuteBulk(ctx context.Context, organizationID uuid.UUID, actorID int, action BulkAction) (result BulkResult, err error) {
	return s.EnqueueBulk(ctx, organizationID, actorID, action)
}

func (s *Service) EnqueueBulk(ctx context.Context, organizationID uuid.UUID, actorID int, action BulkAction) (BulkResult, error) {
	if s == nil || s.pool == nil {
		return BulkResult{}, ErrInvalidBulkAction
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BulkResult{}, fmt.Errorf("begin people bulk enqueue: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := s.EnqueueBulkInTransaction(ctx, tx, organizationID, actorID, action)
	if err != nil {
		return BulkResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BulkResult{}, err
	}
	return result, nil
}

func (s *Service) EnqueueBulkInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actorID int, action BulkAction) (BulkResult, error) {
	if validateBulkAction(action) != nil || s == nil || s.pool == nil || tx == nil || organizationID == uuid.Nil || actorID <= 0 {
		return BulkResult{}, ErrInvalidBulkAction
	}
	actor, err := mutationActor(ctx, actorID)
	if err != nil {
		return BulkResult{}, err
	}
	reference, err := s.parseSelectionReference(action.SelectionToken)
	if err != nil {
		return BulkResult{}, err
	}
	actionKey := action.Kind + ":"
	if action.GroupID != nil {
		actionKey += strconv.Itoa(*action.GroupID)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, reference.String()+":"+actionKey); err != nil {
		return BulkResult{}, fmt.Errorf("lock people bulk idempotency key: %w", err)
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT job_id FROM admin_people_bulk_jobs WHERE organization_id=$1 AND selection_reference=$2 AND action_key=$3`, organizationID, reference, actionKey).Scan(&existing)
	if err == nil {
		return getBulkJobInTransaction(ctx, tx, organizationID, existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BulkResult{}, err
	}
	record, err := loadSelection(ctx, tx.QueryRow(ctx, `SELECT id,organization_id,canonical_filter,snapshot_at,expires_at,account_ids,matched_count,excluded_count,targets FROM admin_people_selections WHERE id=$1 AND organization_id=$2`, reference, organizationID), organizationID)
	if err != nil {
		return BulkResult{}, err
	}
	if !s.now().UTC().Before(record.expires) {
		return BulkResult{}, ErrSelectionExpired
	}
	actor, err = s.resolveBulkActorSnapshot(ctx, tx, organizationID, actor)
	if err != nil {
		return BulkResult{}, err
	}
	if action.Kind == BulkAssignGroup {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM access_groups WHERE organization_id=$1 AND id=$2)`, organizationID, *action.GroupID).Scan(&exists); err != nil {
			return BulkResult{}, err
		}
		if !exists {
			return BulkResult{}, ErrNotFound
		}
	}
	jobID, err := idgen.NextID()
	if err != nil {
		return BulkResult{}, err
	}
	now := s.now().UTC()
	initial := BulkResult{JobID: jobID, Status: "queued", ProgressTotal: len(record.targets), Skipped: []RecordResult{}, Failed: []RecordResult{}}
	requestJSON, _ := json.Marshal(map[string]any{"organization_id": organizationID, "selection_id": reference, "kind": action.Kind, "group_id": action.GroupID})
	resultJSON, _ := json.Marshal(initial)
	if _, err = tx.Exec(ctx, `INSERT INTO admin_jobs (id,job_type,status,created_by_user_id,request_payload,result_payload,message,progress_current,progress_total,requested_at,updated_at) VALUES ($1,'organization_people_bulk','queued',$2,$3,$4,'People bulk operation queued',0,$5,$6,$6)`, jobID, actorID, requestJSON, resultJSON, len(record.targets), now); err != nil {
		return BulkResult{}, fmt.Errorf("persist queued people bulk job: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO admin_people_bulk_jobs (job_id,organization_id,selection_reference,action_kind,group_id,action_key,actor_account_id,actor_authority,actor_membership_id,actor_security_revision,organization_policy_revision,request_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,''))`, jobID, organizationID, reference, action.Kind, action.GroupID, actionKey, actorID, actor.Authority, actor.MembershipID, actor.SecurityRevision, actor.PolicyRevision, actor.RequestID); err != nil {
		return BulkResult{}, err
	}
	rows := make([][]any, 0, len(record.targets))
	for ordinal, target := range record.targets {
		raw, _ := json.Marshal(target)
		rows = append(rows, []any{jobID, target.AccountID, ordinal, raw})
	}
	if len(rows) > 0 {
		if _, err = tx.CopyFrom(ctx, pgx.Identifier{"admin_people_bulk_targets"}, []string{"job_id", "account_id", "ordinal", "snapshot"}, pgx.CopyFromRows(rows)); err != nil {
			return BulkResult{}, fmt.Errorf("persist people bulk targets: %w", err)
		}
	}
	ctx = WithMutationActor(ctx, actor)
	if err = recordPeopleAudit(ctx, tx, actorID, "people.bulk_job_created", "bulk_job", jobID, organizationID, 0, 0, 0, "success", nil, map[string]any{"kind": action.Kind, "matched": len(record.targets), "excluded": record.excluded}); err != nil {
		return BulkResult{}, err
	}
	return initial, nil
}

func (s *Service) EnqueuePolicyBulk(ctx context.Context, organizationID uuid.UUID, actorID int, action PolicyBulkAction) (BulkResult, error) {
	return s.EnqueuePolicyBulkForScope(ctx, organizationID, actorID, action, PolicyOperationScopeOrganization)
}

func (s *Service) EnqueuePolicyBulkForScope(ctx context.Context, organizationID uuid.UUID, actorID int, action PolicyBulkAction, operationScope PolicyOperationScope) (BulkResult, error) {
	if s == nil || s.pool == nil {
		return BulkResult{}, ErrInvalidPolicyCommand
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BulkResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := s.EnqueuePolicyBulkForScopeInTransaction(ctx, tx, organizationID, actorID, action, operationScope)
	if err != nil {
		return BulkResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BulkResult{}, err
	}
	return result, nil
}

func (s *Service) EnqueuePolicyBulkForScopeInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actorID int, action PolicyBulkAction, operationScope PolicyOperationScope) (BulkResult, error) {
	key := strings.TrimSpace(action.IdempotencyKey)
	commandDigest, digestErr := PolicyCommandDigest(action.Command)
	if s == nil || s.pool == nil || tx == nil || organizationID == uuid.Nil || actorID <= 0 || key == "" || len(key) > 128 || strings.TrimSpace(action.SelectionToken) == "" || strings.TrimSpace(action.ConfirmationToken) == "" || digestErr != nil || !validPolicyOperationScope(operationScope) {
		return BulkResult{}, ErrInvalidPolicyCommand
	}
	reference, err := s.parseSelectionReference(action.SelectionToken)
	if err != nil {
		return BulkResult{}, err
	}
	receiptScope := policyOperationScopeBinding{Kind: operationScope}
	if payload, parseErr := s.parsePolicyConfirmation(action.ConfirmationToken); parseErr == nil && payload.Version == policyConfirmationVersion && payload.SelectionID == reference && payload.OperationScope.Kind == operationScope {
		receiptScope = payload.OperationScope
	}
	requestDigest := policyBulkRequestDigest(reference, commandDigest, receiptScope)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, organizationID.String()+":"+strconv.Itoa(actorID)+":"+key); err != nil {
		return BulkResult{}, err
	}
	var priorJob, priorDigest string
	err = tx.QueryRow(ctx, `SELECT job_id,request_digest FROM admin_people_bulk_job_receipts WHERE organization_id=$1 AND actor_account_id=$2 AND idempotency_key=$3`, organizationID, actorID, key).Scan(&priorJob, &priorDigest)
	if err == nil {
		if priorDigest != requestDigest {
			return BulkResult{}, ErrBulkIdempotencyConflict
		}
		return getBulkJobInTransaction(ctx, tx, organizationID, priorJob)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BulkResult{}, err
	}
	confirmation, err := s.validatePolicyConfirmationInTx(ctx, tx, organizationID, actorID, action.SelectionToken, action.Command, action.ConfirmationToken, operationScope)
	if err != nil {
		return BulkResult{}, err
	}
	record, err := s.loadActivePolicySelectionInTx(ctx, tx, organizationID, reference)
	if err != nil {
		return BulkResult{}, err
	}
	store := entitlements.NewTemplateStore(s.pool)
	var target entitlements.CohortRevision
	switch strings.TrimSpace(action.Command.Kind) {
	case PolicyApplyEntitlementTemplate:
		target, _, err = store.EnsureExactCohortInTx(ctx, tx, organizationID, strings.TrimSpace(action.Command.TemplateKey), action.Command.TemplateRevision, actorID)
	case PolicyDeriveEntitlementCohort:
		target, _, err = store.DeriveCohortInTx(ctx, tx, organizationID, action.Command.CohortID, strings.TrimSpace(action.Command.Name), action.Command.Patch, actorID)
	case PolicyAssignEntitlementCohort:
		target, err = store.GetCohortInTx(ctx, tx, organizationID, action.Command.CohortID)
	case PolicyRestoreDefaultEntitlement:
		resolved, resolveErr := s.resolvePolicyTargetInTx(ctx, tx, organizationID, action.Command)
		err = resolveErr
		target = entitlements.CohortRevision{ID: resolved.CohortID, Revision: resolved.CohortRevision, AccessGroupID: resolved.GroupID, PolicyDigest: resolved.PolicyDigest}
	default:
		err = ErrInvalidPolicyCommand
	}
	if err != nil || target.AccessGroupID <= 0 || target.PolicyDigest != confirmation.TargetPolicyDigest {
		if err != nil {
			return BulkResult{}, err
		}
		return BulkResult{}, ErrInvalidPolicyConfirmation
	}
	actionKey := "policy:" + string(operationScope) + ":" + commandDigest
	var existing string
	err = tx.QueryRow(ctx, `SELECT job_id FROM admin_people_bulk_jobs WHERE organization_id=$1 AND selection_reference=$2 AND action_key=$3`, organizationID, reference, actionKey).Scan(&existing)
	if err == nil {
		if _, err := tx.Exec(ctx, `INSERT INTO admin_people_bulk_job_receipts (organization_id,actor_account_id,idempotency_key,request_digest,job_id) VALUES ($1,$2,$3,$4,$5)`, organizationID, actorID, key, requestDigest, existing); err != nil {
			return BulkResult{}, err
		}
		return getBulkJobInTransaction(ctx, tx, organizationID, existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BulkResult{}, err
	}
	jobID, err := idgen.NextID()
	if err != nil {
		return BulkResult{}, err
	}
	payload, err := json.Marshal(action.Command)
	if err != nil {
		return BulkResult{}, ErrInvalidPolicyCommand
	}
	cohortID, cohortRevision := any(nil), any(nil)
	if target.ID != uuid.Nil {
		cohortID, cohortRevision = target.ID, target.Revision
	}
	now := s.now().UTC()
	initial := BulkResult{JobID: jobID, Status: "queued", ProgressTotal: len(record.targets), Skipped: []RecordResult{}, Failed: []RecordResult{}, TargetCohortID: target.ID, TargetCohortRevision: target.Revision, TargetGroupID: target.AccessGroupID}
	requestJSON, _ := json.Marshal(map[string]any{"organization_id": organizationID, "selection_id": reference, "operation_scope": operationScope, "kind": action.Command.Kind, "command_digest": commandDigest})
	resultJSON, _ := json.Marshal(initial)
	if _, err = tx.Exec(ctx, `INSERT INTO admin_jobs (id,job_type,status,created_by_user_id,request_payload,result_payload,message,progress_current,progress_total,requested_at,updated_at) VALUES ($1,'organization_people_policy_bulk','queued',$2,$3,$4,'People policy bulk operation queued',0,$5,$6,$6)`, jobID, actorID, requestJSON, resultJSON, len(record.targets), now); err != nil {
		return BulkResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO admin_people_bulk_jobs (job_id,organization_id,selection_reference,action_kind,group_id,action_key,actor_account_id,actor_authority,actor_membership_id,actor_security_revision,organization_policy_revision,request_id,action_payload,preview_digest,target_cohort_id,target_cohort_revision,target_group_id) VALUES ($1,$2,$3,$4,NULL,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13,$14,$15,$16)`, jobID, organizationID, reference, action.Command.Kind, actionKey, actorID, confirmation.Actor.Authority, confirmation.Actor.MembershipID, confirmation.Actor.SecurityRevision, confirmation.OrganizationPolicyRevision, confirmation.Actor.RequestID, payload, confirmation.TargetPolicyDigest, cohortID, cohortRevision, target.AccessGroupID); err != nil {
		return BulkResult{}, err
	}
	rows := make([][]any, 0, len(record.targets))
	for ordinal, item := range record.targets {
		raw, _ := json.Marshal(item)
		rows = append(rows, []any{jobID, item.AccountID, ordinal, raw})
	}
	if len(rows) > 0 {
		if _, err = tx.CopyFrom(ctx, pgx.Identifier{"admin_people_bulk_targets"}, []string{"job_id", "account_id", "ordinal", "snapshot"}, pgx.CopyFromRows(rows)); err != nil {
			return BulkResult{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO admin_people_bulk_job_receipts (organization_id,actor_account_id,idempotency_key,request_digest,job_id) VALUES ($1,$2,$3,$4,$5)`, organizationID, actorID, key, requestDigest, jobID); err != nil {
		return BulkResult{}, err
	}
	ctx = WithMutationActor(ctx, confirmation.Actor)
	if err = recordPeopleAudit(ctx, tx, actorID, "people.bulk_policy_job_created", "bulk_job", jobID, organizationID, 0, 0, 0, "success", nil, map[string]any{"kind": action.Command.Kind, "matched": len(record.targets), "target_cohort_id": target.ID, "target_group_id": target.AccessGroupID}); err != nil {
		return BulkResult{}, err
	}
	return initial, nil
}

func policyBulkRequestDigest(reference uuid.UUID, commandDigest string, operationScope policyOperationScopeBinding) string {
	raw, _ := json.Marshal(struct {
		SelectionID    uuid.UUID                   `json:"selection_id"`
		CommandDigest  string                      `json:"command_digest"`
		OperationScope policyOperationScopeBinding `json:"operation_scope"`
	}{SelectionID: reference, CommandDigest: commandDigest, OperationScope: operationScope})
	return digestBytes(raw)
}

func (s *Service) ProcessBulkJob(ctx context.Context, organizationID uuid.UUID, jobID string) (BulkResult, error) {
	for {
		if err := ctx.Err(); err != nil {
			return BulkResult{}, err
		}
		result, err := s.ProcessBulkBatch(ctx, organizationID, jobID, defaultBulkBatchSize)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				_ = s.failBulkJob(context.WithoutCancel(ctx), organizationID, jobID, err)
			}
			return BulkResult{}, err
		}
		if result.Status == bulkStatusCompleted || result.Status == bulkStatusFailed || result.Status == bulkStatusCancelled {
			return result, nil
		}
	}
}

func (s *Service) CancelBulkJob(ctx context.Context, organizationID uuid.UUID, actorID int, jobID string) (BulkResult, error) {
	if s == nil || s.pool == nil || organizationID == uuid.Nil || actorID <= 0 || strings.TrimSpace(jobID) == "" {
		return BulkResult{}, ErrNotFound
	}
	actor, err := mutationActor(ctx, actorID)
	if err != nil {
		return BulkResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BulkResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT j.status FROM admin_jobs j JOIN admin_people_bulk_jobs b ON b.job_id=j.id WHERE j.id=$1 AND b.organization_id=$2 FOR UPDATE OF j`, jobID, organizationID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return BulkResult{}, ErrNotFound
	} else if err != nil {
		return BulkResult{}, err
	}
	if status != bulkStatusCompleted && status != bulkStatusFailed && status != bulkStatusCancelled {
		actor, err = s.resolveBulkActorSnapshot(ctx, tx, organizationID, actor)
		if err != nil {
			return BulkResult{}, err
		}
		ctx = WithMutationActor(ctx, actor)
		if _, err := tx.Exec(ctx, `UPDATE admin_people_bulk_jobs SET cancel_requested_at=now() WHERE job_id=$1`, jobID); err != nil {
			return BulkResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE admin_jobs SET status='cancelled',message='People bulk operation cancelled',error_message='',completed_at=now(),heartbeat_at=now(),updated_at=now() WHERE id=$1`, jobID); err != nil {
			return BulkResult{}, err
		}
		// The audit action is a durable external contract and retains its original spelling.
		if err := recordPeopleAudit(ctx, tx, actorID, "people.bulk_job_cancelled", "bulk_job", jobID, organizationID, 0, 0, 0, "success", nil, map[string]any{auditStatusField: bulkStatusCancelled}); err != nil { //nolint:misspell
			return BulkResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return BulkResult{}, err
	}
	return s.GetBulkJob(ctx, organizationID, jobID)
}

func (s *Service) CancelPolicyBulkJob(ctx context.Context, organizationID uuid.UUID, actorID int, jobID string) (BulkResult, error) {
	if s == nil || s.pool == nil || organizationID == uuid.Nil || actorID <= 0 || strings.TrimSpace(jobID) == "" {
		return BulkResult{}, ErrNotFound
	}
	var actionKind string
	err := s.pool.QueryRow(ctx, `SELECT b.action_kind FROM admin_people_bulk_jobs b WHERE b.job_id=$1 AND b.organization_id=$2`, jobID, organizationID).Scan(&actionKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return BulkResult{}, ErrNotFound
	}
	if err != nil {
		return BulkResult{}, err
	}
	if !isPolicyBulkAction(actionKind) {
		return BulkResult{}, ErrBulkJobNotCancellable
	}
	return s.CancelBulkJob(ctx, organizationID, actorID, jobID)
}

func (s *Service) ProcessBulkBatch(ctx context.Context, organizationID uuid.UUID, jobID string, batchSize int) (BulkResult, error) {
	if s == nil || s.pool == nil || organizationID == uuid.Nil || jobID == "" {
		return BulkResult{}, ErrNotFound
	}
	if batchSize <= 0 || batchSize > 500 {
		batchSize = defaultBulkBatchSize
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BulkResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var action BulkAction
	var policyCommand PolicyCommand
	var actionPayload []byte
	var targetCohortID *uuid.UUID
	var targetCohortRevision *int64
	var targetGroupID *int64
	var actor MutationActor
	var status string
	err = tx.QueryRow(ctx, `SELECT j.status,b.action_kind,b.group_id,b.action_payload,b.target_cohort_id,b.target_cohort_revision,b.target_group_id,b.actor_account_id,b.actor_authority,b.actor_membership_id,b.actor_security_revision,b.organization_policy_revision,COALESCE(b.request_id,'') FROM admin_jobs j JOIN admin_people_bulk_jobs b ON b.job_id=j.id WHERE j.id=$1 AND b.organization_id=$2 FOR UPDATE OF j`, jobID, organizationID).Scan(&status, &action.Kind, &action.GroupID, &actionPayload, &targetCohortID, &targetCohortRevision, &targetGroupID, &actor.AccountID, &actor.Authority, &actor.MembershipID, &actor.SecurityRevision, &actor.PolicyRevision, &actor.RequestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return BulkResult{}, ErrNotFound
	}
	if err != nil {
		return BulkResult{}, err
	}
	ctx = WithMutationActor(ctx, actor)
	if status == bulkStatusCompleted || status == bulkStatusFailed || status == bulkStatusCancelled {
		_ = tx.Commit(ctx)
		return s.GetBulkJob(ctx, organizationID, jobID)
	}
	var runnable bool
	if err := tx.QueryRow(ctx, `SELECT next_attempt_at<=now() AND cancel_requested_at IS NULL FROM admin_people_bulk_jobs WHERE job_id=$1`, jobID).Scan(&runnable); err != nil {
		return BulkResult{}, err
	}
	if !runnable {
		if err := tx.Commit(ctx); err != nil {
			return BulkResult{}, err
		}
		return s.GetBulkJob(ctx, organizationID, jobID)
	}
	isPolicy := isPolicyBulkAction(action.Kind)
	if isPolicy && (targetGroupID == nil || *targetGroupID <= 0 || json.Unmarshal(actionPayload, &policyCommand) != nil) {
		return BulkResult{}, fmt.Errorf("decode policy bulk action")
	}
	if err := s.revalidateBulkActor(ctx, tx, organizationID, actor); err != nil {
		_ = tx.Rollback(ctx)
		_ = s.failBulkJob(context.WithoutCancel(ctx), organizationID, jobID, err)
		return BulkResult{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE admin_jobs SET status='running',started_at=COALESCE(started_at,now()),heartbeat_at=now(),updated_at=now() WHERE id=$1`, jobID); err != nil {
		return BulkResult{}, err
	}
	rows, err := tx.Query(ctx, `SELECT account_id,snapshot FROM admin_people_bulk_targets WHERE job_id=$1 AND status='pending' ORDER BY ordinal FOR UPDATE SKIP LOCKED LIMIT $2`, jobID, batchSize)
	if err != nil {
		return BulkResult{}, err
	}
	type pendingTarget struct {
		accountID int
		snapshot  targetSnapshot
	}
	pending := []pendingTarget{}
	for rows.Next() {
		var item pendingTarget
		var raw []byte
		if err := rows.Scan(&item.accountID, &raw); err != nil {
			rows.Close()
			return BulkResult{}, err
		}
		if json.Unmarshal(raw, &item.snapshot) != nil || item.snapshot.AccountID != item.accountID || item.snapshot.AccountID <= 0 || item.snapshot.MembershipID == uuid.Nil || item.snapshot.MembershipRevision <= 0 || item.snapshot.AccountPolicyRevision <= 0 {
			rows.Close()
			return BulkResult{}, fmt.Errorf("decode bulk target snapshot")
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return BulkResult{}, err
	}
	rows.Close()
	for index, item := range pending {
		savepoint := fmt.Sprintf("bulk_target_%d", index)
		_, _ = tx.Exec(ctx, "SAVEPOINT "+savepoint)
		outcome, reason, mutationErr := "skipped", ReasonActorAuthorityTarget, error(nil)
		if item.accountID != actor.AccountID {
			if isPolicy {
				outcome, reason, mutationErr = s.executePolicyBulkRecord(ctx, tx, organizationID, actor.AccountID, jobID, item.snapshot, policyCommand, *targetGroupID, targetCohortID, targetCohortRevision)
			} else {
				outcome, reason, mutationErr = s.executeBulkRecord(ctx, tx, organizationID, actor.AccountID, item.snapshot, action)
			}
		}
		if mutationErr != nil {
			_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+savepoint)
			outcome = "failed"
			reason = ReasonMutationFailed
		}
		if outcome != "succeeded" {
			auditOutcome := "conflict"
			actionName := "people.bulk_record_skipped"
			if outcome == "failed" {
				auditOutcome = "failure"
				actionName = "people.bulk_record_failed"
			}
			if err := recordPeopleAudit(ctx, tx, actor.AccountID, actionName, "membership", item.snapshot.MembershipID.String(), organizationID, item.accountID, item.snapshot.MembershipRevision, item.snapshot.MembershipRevision, auditOutcome, map[string]any{"snapshot": item.snapshot}, map[string]any{"reason": reason}); err != nil {
				return BulkResult{}, err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE admin_people_bulk_targets SET status=$3,reason=$4,attempts=attempts+1,updated_at=now() WHERE job_id=$1 AND account_id=$2`, jobID, item.accountID, outcome, reason); err != nil {
			return BulkResult{}, err
		}
		_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT "+savepoint)
	}
	result, remaining, err := bulkResultInTx(ctx, tx, jobID)
	if err != nil {
		return BulkResult{}, err
	}
	previous := status
	if remaining == 0 {
		result.Status = "completed"
		result.ProgressCurrent = result.ProgressTotal
		resultJSON, _ := json.Marshal(result)
		if _, err = tx.Exec(ctx, `UPDATE admin_jobs SET status='completed',result_payload=$2,message='People bulk operation completed',progress_current=progress_total,completed_at=now(),heartbeat_at=now(),updated_at=now() WHERE id=$1`, jobID, resultJSON); err != nil {
			return BulkResult{}, err
		}
		if previous != "completed" {
			outcome := "success"
			if len(result.Skipped) > 0 || len(result.Failed) > 0 {
				outcome = "partial_failure"
			}
			if err = recordPeopleAudit(ctx, tx, actor.AccountID, "people.bulk_job_completed", "bulk_job", jobID, organizationID, 0, 0, 0, outcome, nil, map[string]any{"succeeded": result.Succeeded, "skipped": len(result.Skipped), "failed": len(result.Failed)}); err != nil {
				return BulkResult{}, err
			}
		}
	} else {
		result.Status = "running"
		resultJSON, _ := json.Marshal(result)
		if _, err = tx.Exec(ctx, `UPDATE admin_jobs SET result_payload=$2,progress_current=$3,heartbeat_at=now(),updated_at=now() WHERE id=$1`, jobID, resultJSON, result.ProgressCurrent); err != nil {
			return BulkResult{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return BulkResult{}, err
	}
	return result, nil
}

func (s *Service) resolveBulkActorSnapshot(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actor MutationActor) (MutationActor, error) {
	var policyRevision int64
	if err := tx.QueryRow(ctx, `SELECT policy_revision FROM organizations WHERE id=$1 AND status='active'`, organizationID).Scan(&policyRevision); err != nil {
		return MutationActor{}, ErrAuthorizationStateChanged
	}
	if actor.PolicyRevision <= 0 {
		actor.PolicyRevision = policyRevision
	} else if actor.PolicyRevision != policyRevision {
		return MutationActor{}, ErrAuthorizationStateChanged
	}

	if actor.Authority == AuthorityPlatformAdmin {
		var enabled bool
		var role string
		if err := tx.QueryRow(ctx, `SELECT enabled,role FROM users WHERE id=$1`, actor.AccountID).Scan(&enabled, &role); err != nil || !enabled || role != "admin" {
			return MutationActor{}, ErrAuthorizationStateChanged
		}
		return actor, nil
	}

	var membershipID uuid.UUID
	var status tenancy.MembershipStatus
	var role string
	var securityRevision int64
	if err := tx.QueryRow(ctx, `SELECT id,status,legacy_role,security_revision FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, organizationID, actor.AccountID).Scan(&membershipID, &status, &role, &securityRevision); err != nil {
		return MutationActor{}, ErrAuthorizationStateChanged
	}
	if status != tenancy.MembershipActive || role != "admin" {
		return MutationActor{}, ErrAuthorizationStateChanged
	}
	if actor.MembershipID == uuid.Nil && actor.SecurityRevision <= 0 {
		actor.MembershipID = membershipID
		actor.SecurityRevision = securityRevision
	} else if actor.MembershipID != membershipID || actor.SecurityRevision != securityRevision {
		return MutationActor{}, ErrAuthorizationStateChanged
	}
	return actor, nil
}

func (s *Service) revalidateBulkActor(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actor MutationActor) error {
	var policyRevision int64
	err := tx.QueryRow(ctx, `SELECT policy_revision FROM organizations WHERE id=$1 AND status='active' FOR UPDATE`, organizationID).Scan(&policyRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAuthorizationStateChanged
	}
	if err != nil {
		return err
	}
	if policyRevision != actor.PolicyRevision {
		return ErrAuthorizationStateChanged
	}
	if actor.Authority == AuthorityOrganizationAdmin {
		var status tenancy.MembershipStatus
		var role string
		var revision int64
		err = tx.QueryRow(ctx, `SELECT status,legacy_role,security_revision FROM organization_memberships WHERE id=$1 AND organization_id=$2 AND account_id=$3 FOR UPDATE`, actor.MembershipID, organizationID, actor.AccountID).Scan(&status, &role, &revision)
		if err != nil || status != tenancy.MembershipActive || role != "admin" || revision != actor.SecurityRevision {
			return ErrAuthorizationStateChanged
		}
		return nil
	}
	var enabled bool
	var role string
	err = tx.QueryRow(ctx, `SELECT enabled,role FROM users WHERE id=$1 FOR UPDATE`, actor.AccountID).Scan(&enabled, &role)
	if err != nil || !enabled || role != "admin" {
		return ErrAuthorizationStateChanged
	}
	return nil
}

func (s *Service) failBulkJob(ctx context.Context, organizationID uuid.UUID, jobID string, cause error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var actor MutationActor
	var status string
	err = tx.QueryRow(ctx, `SELECT j.status,b.actor_account_id,b.actor_authority,b.actor_membership_id,b.actor_security_revision,b.organization_policy_revision,COALESCE(b.request_id,'') FROM admin_jobs j JOIN admin_people_bulk_jobs b ON b.job_id=j.id WHERE j.id=$1 AND b.organization_id=$2 FOR UPDATE OF j`, jobID, organizationID).Scan(&status, &actor.AccountID, &actor.Authority, &actor.MembershipID, &actor.SecurityRevision, &actor.PolicyRevision, &actor.RequestID)
	if err != nil {
		return err
	}
	if status == bulkStatusCompleted || status == bulkStatusFailed || status == bulkStatusCancelled {
		return tx.Commit(ctx)
	}
	ctx = WithMutationActor(ctx, actor)
	result, _, loadErr := bulkResultInTx(ctx, tx, jobID)
	if loadErr != nil {
		return loadErr
	}
	result.Status = "failed"
	raw, _ := json.Marshal(result)
	if _, err = tx.Exec(ctx, `UPDATE admin_jobs SET status='failed',result_payload=$2,message='People bulk operation failed',error_message='executor_failure',completed_at=now(),heartbeat_at=now(),updated_at=now() WHERE id=$1`, jobID, raw); err != nil {
		return err
	}
	if err = recordPeopleAudit(ctx, tx, actor.AccountID, "people.bulk_job_failed", "bulk_job", jobID, organizationID, 0, 0, 0, "failure", nil, map[string]any{"error": "executor_failure"}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) FailBulkJob(ctx context.Context, organizationID uuid.UUID, jobID string, cause error) error {
	if s == nil || s.pool == nil || organizationID == uuid.Nil || strings.TrimSpace(jobID) == "" {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	var attempts int
	var actor MutationActor
	if err := tx.QueryRow(ctx, `SELECT j.status,b.attempt_count,b.actor_account_id,b.actor_authority,b.actor_membership_id,b.actor_security_revision,b.organization_policy_revision,COALESCE(b.request_id,'') FROM admin_jobs j JOIN admin_people_bulk_jobs b ON b.job_id=j.id WHERE j.id=$1 AND b.organization_id=$2 FOR UPDATE OF j,b`, jobID, organizationID).Scan(&status, &attempts, &actor.AccountID, &actor.Authority, &actor.MembershipID, &actor.SecurityRevision, &actor.PolicyRevision, &actor.RequestID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status == bulkStatusCompleted || status == bulkStatusFailed || status == bulkStatusCancelled {
		return tx.Commit(ctx)
	}
	attempts++
	if attempts >= 5 {
		if _, err := tx.Exec(ctx, `UPDATE admin_people_bulk_jobs SET attempt_count=$2,last_error_code='executor_failure',next_attempt_at=now() WHERE job_id=$1`, jobID, attempts); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE admin_jobs SET status='failed',message='People bulk operation failed',error_message='executor_failure',completed_at=now(),heartbeat_at=now(),updated_at=now() WHERE id=$1`, jobID); err != nil {
			return err
		}
		ctx = WithMutationActor(ctx, actor)
		if err := recordPeopleAudit(ctx, tx, actor.AccountID, "people.bulk_job_failed", "bulk_job", jobID, organizationID, 0, 0, 0, "failure", nil, map[string]any{"error": "executor_failure", "attempts": attempts}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	backoff := time.Duration(1<<uint(attempts-1)) * time.Second
	if _, err := tx.Exec(ctx, `UPDATE admin_people_bulk_jobs SET attempt_count=$2,last_error_code='executor_failure',next_attempt_at=now()+$3::interval WHERE job_id=$1`, jobID, attempts, backoff.String()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE admin_jobs SET status='queued',message='People bulk operation retry scheduled',error_message='executor_failure',heartbeat_at=now(),updated_at=now() WHERE id=$1`, jobID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func bulkResultInTx(ctx context.Context, tx pgx.Tx, jobID string) (BulkResult, int, error) {
	var result BulkResult
	result.JobID = jobID
	result.Skipped = []RecordResult{}
	result.Failed = []RecordResult{}
	var remaining int
	var cohortID *uuid.UUID
	var cohortRevision *int64
	var groupID *int64
	if err := tx.QueryRow(ctx, `SELECT target_cohort_id,target_cohort_revision,target_group_id FROM admin_people_bulk_jobs WHERE job_id=$1`, jobID).Scan(&cohortID, &cohortRevision, &groupID); err != nil {
		return result, 0, err
	}
	if cohortID != nil {
		result.TargetCohortID = *cohortID
	}
	if cohortRevision != nil {
		result.TargetCohortRevision = *cohortRevision
	}
	if groupID != nil {
		result.TargetGroupID = *groupID
	}
	if err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE status<>'pending'),count(*) FILTER (WHERE status='succeeded'),count(*) FILTER (WHERE status='pending') FROM admin_people_bulk_targets WHERE job_id=$1`, jobID).Scan(&result.ProgressTotal, &result.ProgressCurrent, &result.Succeeded, &remaining); err != nil {
		return result, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT account_id,status,reason FROM admin_people_bulk_targets WHERE job_id=$1 AND status IN ('skipped','failed') ORDER BY ordinal`, jobID)
	if err != nil {
		return result, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var record RecordResult
		var status string
		if err := rows.Scan(&record.AccountID, &status, &record.Reason); err != nil {
			return result, 0, err
		}
		if status == "skipped" {
			result.Skipped = append(result.Skipped, record)
		} else {
			result.Failed = append(result.Failed, record)
		}
	}
	return result, remaining, rows.Err()
}

func (s *Service) executeBulkRecord(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actorID int, snapshot targetSnapshot, action BulkAction) (string, string, error) {
	var membershipID uuid.UUID
	var status tenancy.MembershipStatus
	var revision, accountRevision int64
	var protected bool
	err := tx.QueryRow(ctx, `SELECT m.id,m.status,m.security_revision,m.access_policy_revision,(o.owner_account_id=m.account_id) FROM organization_memberships m JOIN organizations o ON o.id=m.organization_id JOIN users u ON u.id=m.account_id WHERE m.organization_id=$1 AND m.account_id=$2 FOR UPDATE OF m`, organizationID, snapshot.AccountID).Scan(&membershipID, &status, &revision, &accountRevision, &protected)
	if errors.Is(err, pgx.ErrNoRows) {
		return "failed", ReasonNotFound, nil
	}
	if err != nil {
		return "", "", err
	}
	if membershipID != snapshot.MembershipID || revision != snapshot.MembershipRevision || accountRevision != snapshot.AccountPolicyRevision || status != snapshot.MembershipStatus {
		return "skipped", ReasonAuthorizationChanged, nil
	}
	switch action.Kind {
	case BulkAssignGroup:
		rows, err := tx.Query(ctx, `SELECT id,access_group_id,(access_group_id=$3),updated_at FROM user_profiles WHERE organization_id=$1 AND user_id=$2 ORDER BY id FOR UPDATE`, organizationID, snapshot.AccountID, snapshot.GroupID)
		if err != nil {
			return "", "", err
		}
		profiles := []profileSnapshot{}
		for rows.Next() {
			var profile profileSnapshot
			if err := rows.Scan(&profile.ID, &profile.GroupID, &profile.InheritsAccount, &profile.UpdatedAt); err != nil {
				rows.Close()
				return "", "", err
			}
			profiles = append(profiles, profile)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", "", err
		}
		rows.Close()
		if !sameProfileSnapshots(profiles, snapshot.Profiles) {
			return "skipped", ReasonAuthorizationChanged, nil
		}
		total := len(profiles)
		changed := 0
		for _, profile := range profiles {
			if profile.GroupID != *action.GroupID {
				changed++
			}
		}
		if total == 0 {
			return "skipped", ReasonNoProfiles, nil
		}
		if changed == 0 {
			return "skipped", ReasonAlreadyApplied, nil
		}
		if _, err := tx.Exec(ctx, `UPDATE user_profiles SET access_group_id=$3,updated_at=now() WHERE organization_id=$1 AND user_id=$2 AND access_group_id<>$3`, organizationID, snapshot.AccountID, *action.GroupID); err != nil {
			return "", "", err
		}
		if err := bumpPersonRevisions(ctx, tx, membershipID, snapshot.AccountID); err != nil {
			return "", "", err
		}
		if err := recordPeopleAudit(ctx, tx, actorID, "people.bulk_group_assigned", "membership", membershipID.String(), organizationID, snapshot.AccountID, revision, revision+1, "success", map[string]any{"changed_profiles": changed}, map[string]any{"group_id": *action.GroupID, "changed_profiles": changed}); err != nil {
			return "", "", err
		}
		return "succeeded", "", nil
	case BulkSuspendMemberships, BulkReactivateMemberships:
		target := tenancy.MembershipSuspended
		actionName := "people.bulk_membership_suspended"
		if action.Kind == BulkReactivateMemberships {
			target = tenancy.MembershipActive
			actionName = "people.bulk_membership_reactivated"
		}
		if target == tenancy.MembershipSuspended && protected {
			return "skipped", ReasonProtectedOwner, nil
		}
		if status == target {
			return "skipped", ReasonAlreadyApplied, nil
		}
		if _, err := tx.Exec(ctx, `UPDATE organization_memberships SET status=$2,security_revision=security_revision+1,updated_at=now() WHERE id=$1`, membershipID, target); err != nil {
			return "", "", err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_writer',
				CASE WHEN (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized'
				     THEN 'v1' ELSE '' END, true)`); err != nil {
			return "", "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE organization_memberships SET access_policy_revision=access_policy_revision+1,updated_at=now() WHERE id=$1`, membershipID); err != nil {
			return "", "", err
		}
		if err := recordPeopleAudit(ctx, tx, actorID, actionName, "membership", membershipID.String(), organizationID, snapshot.AccountID, revision, revision+1, "success", map[string]any{auditStatusField: status}, map[string]any{auditStatusField: target}); err != nil {
			return "", "", err
		}
		return "succeeded", "", nil
	}
	return "", "", ErrInvalidBulkAction
}

func (s *Service) executePolicyBulkRecord(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actorID int, jobID string, snapshot targetSnapshot, command PolicyCommand, targetGroupID int64, targetCohortID *uuid.UUID, targetCohortRevision *int64) (string, string, error) {
	var membershipID uuid.UUID
	var status tenancy.MembershipStatus
	var membershipRevision, accountRevision, currentGroupID, currentCohortRevision int64
	var currentCohortID uuid.UUID
	var sourceKey string
	var sourceRevision int64
	var hasManagedOverrides bool
	err := tx.QueryRow(ctx, `
		SELECT m.id,m.status,m.security_revision,m.access_policy_revision,COALESCE(m.access_group_id,0),
		       COALESCE(g.managed_cohort_id,'00000000-0000-0000-0000-000000000000'::uuid),COALESCE(r.revision,0),
		       COALESCE(r.source_template_key,g.managed_template_key,''),COALESCE(r.source_template_revision,g.managed_template_revision,0),
		       m.library_ids IS NOT NULL OR m.max_playback_quality IS NOT NULL OR
		       m.max_streams IS NOT NULL OR m.max_transcodes IS NOT NULL OR
		       m.max_profiles IS DISTINCT FROM CASE
		           WHEN g.max_profiles > 0 THEN g.max_profiles
		           WHEN g.managed_template_key IS NOT NULL OR g.managed_cohort_id IS NOT NULL THEN 1
		           ELSE m.max_profiles
		       END OR
		       m.transcode_allowed IS NOT NULL OR m.audio_transcode_allowed IS NOT NULL OR
		       m.download_allowed IS NOT NULL OR m.download_transcode_allowed IS NOT NULL OR
		       m.requests_allowed IS NOT NULL
		FROM organization_memberships m
		JOIN users u ON u.id=m.account_id
		LEFT JOIN access_groups g ON g.organization_id=m.organization_id AND g.id=m.access_group_id
		LEFT JOIN entitlement_policy_cohort_revisions r ON r.organization_id=g.organization_id AND r.id=g.managed_cohort_id AND r.access_group_id=g.id
		WHERE m.organization_id=$1 AND m.account_id=$2 FOR UPDATE OF m`, organizationID, snapshot.AccountID).Scan(
		&membershipID, &status, &membershipRevision, &accountRevision, &currentGroupID,
		&currentCohortID, &currentCohortRevision, &sourceKey, &sourceRevision, &hasManagedOverrides,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return "failed", ReasonNotFound, nil
	}
	if err != nil {
		return "", "", err
	}
	if membershipID != snapshot.MembershipID || status != tenancy.MembershipActive || status != snapshot.MembershipStatus || membershipRevision != snapshot.MembershipRevision || accountRevision != snapshot.AccountPolicyRevision || currentGroupID != snapshot.GroupID || currentCohortID != snapshot.CohortID || currentCohortRevision != snapshot.CohortRevision || sourceKey != snapshot.SourceTemplateKey || sourceRevision != snapshot.SourceTemplateRevision {
		return "skipped", ReasonAuthorizationChanged, nil
	}
	rows, err := tx.Query(ctx, `SELECT id,access_group_id,(access_group_id=$3),updated_at FROM user_profiles WHERE organization_id=$1 AND user_id=$2 ORDER BY id FOR UPDATE`, organizationID, snapshot.AccountID, currentGroupID)
	if err != nil {
		return "", "", err
	}
	profiles := []profileSnapshot{}
	for rows.Next() {
		var profile profileSnapshot
		if err := rows.Scan(&profile.ID, &profile.GroupID, &profile.InheritsAccount, &profile.UpdatedAt); err != nil {
			rows.Close()
			return "", "", err
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", "", err
	}
	rows.Close()
	if !sameProfileSnapshots(profiles, snapshot.Profiles) {
		return "skipped", ReasonAuthorizationChanged, nil
	}
	profilesMoved := 0
	for _, profile := range profiles {
		if profile.GroupID != int(targetGroupID) && (profile.InheritsAccount || command.IncludeCustomProfiles) {
			profilesMoved++
		}
	}
	if currentGroupID == targetGroupID && profilesMoved == 0 && !hasManagedOverrides {
		return "skipped", ReasonAlreadyApplied, nil
	}
	if command.IncludeCustomProfiles {
		if _, err := tx.Exec(ctx, `UPDATE user_profiles SET access_group_id=$3,updated_at=now() WHERE organization_id=$1 AND user_id=$2 AND access_group_id<>$3`, organizationID, snapshot.AccountID, targetGroupID); err != nil {
			return "", "", err
		}
	} else if _, err := tx.Exec(ctx, `UPDATE user_profiles SET access_group_id=$3,updated_at=now() WHERE organization_id=$1 AND user_id=$2 AND access_group_id=$4 AND access_group_id<>$3`, organizationID, snapshot.AccountID, targetGroupID, currentGroupID); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_writer',
				CASE WHEN (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized'
				     THEN 'v1' ELSE '' END, true)`); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE organization_memberships AS memberships SET
			access_group_id=$2,
			max_profiles=(SELECT CASE
				WHEN groups.max_profiles > 0 THEN groups.max_profiles
				WHEN groups.managed_template_key IS NOT NULL OR groups.managed_cohort_id IS NOT NULL THEN 1
				ELSE memberships.max_profiles
			END FROM access_groups AS groups WHERE groups.organization_id=$3 AND groups.id=$2),
			library_ids=NULL,max_playback_quality=NULL,max_streams=NULL,max_transcodes=NULL,
			transcode_allowed=NULL,audio_transcode_allowed=NULL,download_allowed=NULL,
			download_transcode_allowed=NULL,requests_allowed=NULL,
			access_policy_revision=access_policy_revision+1,updated_at=now()
		WHERE memberships.account_id=$1 AND memberships.organization_id=$3`,
		snapshot.AccountID, targetGroupID, organizationID); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE organization_memberships SET security_revision=security_revision+1,updated_at=now() WHERE id=$1`, membershipID); err != nil {
		return "", "", err
	}
	cohortID, cohortRevision := uuid.Nil, int64(0)
	if targetCohortID != nil {
		cohortID = *targetCohortID
	}
	if targetCohortRevision != nil {
		cohortRevision = *targetCohortRevision
	}
	policyResults, _, err := entitlements.NewTemplateStore(s.pool).GetAccountPoliciesInTx(ctx, tx, organizationID, []int{snapshot.AccountID})
	if err != nil || len(policyResults) != 1 || policyResults[0].Snapshot == nil {
		if err != nil {
			return "", "", err
		}
		return "", "", errors.New("reconcile assigned policy")
	}
	effectivePolicy := policyResults[0].Snapshot.Policy
	var targetPolicy entitlements.Policy
	var targetAudioTranscodeAllowed bool
	err = tx.QueryRow(ctx, `
		SELECT library_ids,playback_allowed,max_streams,max_profiles,transcode_allowed,
		       max_transcodes,download_allowed,download_transcode_allowed,max_playback_quality,
		       allowed_permissions,requests_allowed,audio_transcode_allowed
		FROM access_groups WHERE organization_id=$1 AND id=$2`, organizationID, targetGroupID).Scan(
		&targetPolicy.LibraryIDs, &targetPolicy.PlaybackAllowed, &targetPolicy.MaxStreams, &targetPolicy.MaxProfiles,
		&targetPolicy.TranscodeAllowed, &targetPolicy.MaxTranscodes, &targetPolicy.DownloadAllowed,
		&targetPolicy.DownloadTranscodeAllowed, &targetPolicy.MaxPlaybackQuality,
		&targetPolicy.AllowedPermissions, &targetPolicy.RequestsAllowed, &targetAudioTranscodeAllowed,
	)
	if err != nil {
		return "", "", err
	}
	targetPolicy, err = materializePreviewPolicyWithDB(ctx, tx, targetPolicy)
	if err != nil {
		return "", "", err
	}
	var accountPermissions []string
	var accountMaxProfiles int
	if err := tx.QueryRow(ctx, `SELECT permissions,max_profiles FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, organizationID, snapshot.AccountID).Scan(&accountPermissions, &accountMaxProfiles); err != nil {
		return "", "", err
	}
	targetEffectivePolicy := accesspolicy.ApplyGroupPolicy(
		&models.User{Permissions: accountPermissions, MaxProfiles: accountMaxProfiles},
		&accesspolicy.GroupPolicy{
			LibraryIDs: targetPolicy.LibraryIDs, PlaybackAllowed: targetPolicy.PlaybackAllowed,
			MaxStreams: targetPolicy.MaxStreams, MaxProfiles: targetPolicy.MaxProfiles,
			TranscodeAllowed: targetPolicy.TranscodeAllowed, AudioTranscodeAllowed: targetAudioTranscodeAllowed,
			MaxTranscodes: targetPolicy.MaxTranscodes, DownloadAllowed: targetPolicy.DownloadAllowed,
			DownloadTranscodeAllowed: targetPolicy.DownloadTranscodeAllowed,
			MaxPlaybackQuality:       targetPolicy.MaxPlaybackQuality,
			AllowedPermissions:       targetPolicy.AllowedPermissions, RequestsAllowed: targetPolicy.RequestsAllowed,
		},
	)
	if !managedEffectivePolicyMatchesTarget(effectivePolicy, targetEffectivePolicy) {
		return "", "", errors.New("reconcile assigned policy")
	}
	effectivePolicyDigest, err := entitlements.EffectivePolicyDigest(effectivePolicy)
	if err != nil {
		return "", "", err
	}
	before := map[string]any{"group_id": currentGroupID, "cohort_id": currentCohortID, "cohort_revision": currentCohortRevision}
	after := map[string]any{
		"job_id": jobID, "group_id": targetGroupID, "cohort_id": cohortID, "cohort_revision": cohortRevision,
		"profiles_moved": profilesMoved, "include_custom_profiles": command.IncludeCustomProfiles,
		"managed_account_overrides_cleared": hasManagedOverrides,
		"effective_policy":                  effectivePolicy, "effective_policy_digest": effectivePolicyDigest,
	}
	if err := recordPeopleAudit(ctx, tx, actorID, "people.bulk_policy_assigned", "membership", membershipID.String(), organizationID, snapshot.AccountID, membershipRevision, membershipRevision+1, "success", before, after); err != nil {
		return "", "", err
	}
	if err := recordPeopleAudit(ctx, tx, actorID, "entitlement.policy_assignment", "membership", membershipID.String(), organizationID, snapshot.AccountID, accountRevision, accountRevision+1, "success", before, after); err != nil {
		return "", "", err
	}
	return "succeeded", "", nil
}

func managedEffectivePolicyMatchesTarget(effective entitlements.EffectivePolicySnapshot, target accesspolicy.EffectiveUserPolicy) bool {
	return reflect.DeepEqual(effective.LibraryIDs, target.LibraryIDs) &&
		effective.PlaybackAllowed == target.PlaybackAllowed &&
		effective.MaxStreams == target.MaxStreams &&
		effective.MaxProfiles == target.MaxProfiles &&
		effective.MaxTranscodes == target.MaxTranscodes &&
		effective.TranscodeAllowed == target.TranscodeAllowed &&
		effective.AudioTranscodeAllowed == target.AudioTranscodeAllowed &&
		effective.DownloadAllowed == target.DownloadAllowed &&
		effective.DownloadTranscodeAllowed == target.DownloadTranscodeAllowed &&
		effective.MaxPlaybackQuality == target.MaxPlaybackQuality &&
		reflect.DeepEqual(effective.AllowedPermissions, target.Permissions) &&
		effective.RequestsAllowed == target.RequestsAllowed
}

func sameProfileSnapshots(current, want []profileSnapshot) bool {
	if len(current) != len(want) {
		return false
	}
	for i := range current {
		if current[i].ID != want[i].ID || current[i].GroupID != want[i].GroupID || current[i].InheritsAccount != want[i].InheritsAccount || !current[i].UpdatedAt.Equal(want[i].UpdatedAt) {
			return false
		}
	}
	return true
}

func bumpPersonRevisions(ctx context.Context, tx pgx.Tx, membershipID uuid.UUID, accountID int) error {
	if _, err := tx.Exec(ctx, `UPDATE organization_memberships SET security_revision=security_revision+1,updated_at=now() WHERE id=$1`, membershipID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_writer',
				CASE WHEN (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized'
				     THEN 'v1' ELSE '' END, true)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE organization_memberships SET access_policy_revision=access_policy_revision+1,updated_at=now() WHERE id=$1`, membershipID); err != nil {
		return err
	}
	return nil
}

func recordPeopleAudit(ctx context.Context, tx pgx.Tx, actorID int, action, targetType, targetID string, organizationID uuid.UUID, accountID int, beforeRevision, afterRevision int64, outcome string, before, after map[string]any) error {
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	actor, err := mutationActor(ctx, actorID)
	if err != nil {
		return err
	}
	subject := ""
	if accountID > 0 {
		subject = strconv.Itoa(accountID)
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_audit_events (actor_account_id,actor_platform_role,authority_context,action,target_type,target_id,organization_id,subject_id,before_revision,after_revision,outcome,request_id,before_state,after_state) VALUES ($1,$2,'organization',$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,NULLIF($11,''),$12,$13)`, actorID, actor.Authority, action, targetType, targetID, organizationID, subject, beforeRevision, afterRevision, outcome, actor.RequestID, beforeJSON, afterJSON)
	if err != nil {
		return fmt.Errorf("record people admin audit: %w", err)
	}
	return nil
}

func (s *Service) GetBulkJob(ctx context.Context, organizationID uuid.UUID, jobID string) (BulkResult, error) {
	if s == nil || s.pool == nil {
		return BulkResult{}, fmt.Errorf("people store unavailable")
	}
	if organizationID == uuid.Nil || strings.TrimSpace(jobID) == "" {
		return BulkResult{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BulkResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := getBulkJobInTransaction(ctx, tx, organizationID, jobID)
	if err != nil {
		return BulkResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return BulkResult{}, err
	}
	return result, nil
}

func getBulkJobInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, jobID string) (BulkResult, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT j.status FROM admin_jobs j JOIN admin_people_bulk_jobs b ON b.job_id=j.id WHERE j.id=$1 AND b.organization_id=$2`, jobID, organizationID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return BulkResult{}, ErrNotFound
	}
	if err != nil {
		return BulkResult{}, fmt.Errorf("load people bulk job: %w", err)
	}
	result, _, err := bulkResultInTx(ctx, tx, jobID)
	if err != nil {
		return BulkResult{}, err
	}
	result.Status = status
	return result, nil
}

func (s *Service) GetPolicyBulkJob(ctx context.Context, organizationID uuid.UUID, jobID string) (BulkResult, error) {
	if s == nil || s.pool == nil || organizationID == uuid.Nil || strings.TrimSpace(jobID) == "" {
		return BulkResult{}, ErrNotFound
	}
	var actionKind string
	err := s.pool.QueryRow(ctx, `SELECT b.action_kind FROM admin_people_bulk_jobs b WHERE b.job_id=$1 AND b.organization_id=$2`, jobID, organizationID).Scan(&actionKind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !isPolicyBulkAction(actionKind)) {
		return BulkResult{}, ErrNotFound
	}
	if err != nil {
		return BulkResult{}, err
	}
	return s.GetBulkJob(ctx, organizationID, jobID)
}

func isPolicyBulkAction(kind string) bool {
	return kind == PolicyAssignEntitlementCohort || kind == PolicyApplyEntitlementTemplate || kind == PolicyDeriveEntitlementCohort || kind == PolicyRestoreDefaultEntitlement
}

func (s *Service) UpdateMembership(ctx context.Context, organizationID uuid.UUID, actorID, accountID int, expectedRevision int64, status tenancy.MembershipStatus) (PersonSummary, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PersonSummary{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	person, err := s.UpdateMembershipInTransaction(ctx, tx, organizationID, actorID, accountID, expectedRevision, status)
	if err != nil {
		return PersonSummary{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PersonSummary{}, err
	}
	return person, nil
}

func (s *Service) UpdateMembershipInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actorID, accountID int, expectedRevision int64, status tenancy.MembershipStatus) (PersonSummary, error) {
	if status != tenancy.MembershipActive && status != tenancy.MembershipSuspended {
		return PersonSummary{}, ErrInvalidBulkAction
	}
	var err error
	var id uuid.UUID
	var current tenancy.MembershipStatus
	var revision int64
	var protected bool
	err = tx.QueryRow(ctx, `SELECT m.id,m.status,m.security_revision,(o.owner_account_id=m.account_id) FROM organization_memberships m JOIN organizations o ON o.id=m.organization_id WHERE m.organization_id=$1 AND m.account_id=$2 FOR UPDATE OF m`, organizationID, accountID).Scan(&id, &current, &revision, &protected)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonSummary{}, ErrNotFound
	}
	if err != nil {
		return PersonSummary{}, err
	}
	if revision != expectedRevision {
		return PersonSummary{}, ErrAuthorizationStateChanged
	}
	if protected && status == tenancy.MembershipSuspended {
		return PersonSummary{}, ErrInvalidBulkAction
	}
	if current != status {
		if _, err = tx.Exec(ctx, `UPDATE organization_memberships SET status=$2,security_revision=security_revision+1,updated_at=now() WHERE id=$1`, id, status); err != nil {
			return PersonSummary{}, err
		}
		if _, err = tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_writer',
				CASE WHEN (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized'
				     THEN 'v1' ELSE '' END, true)`); err != nil {
			return PersonSummary{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE organization_memberships SET access_policy_revision=access_policy_revision+1,updated_at=now() WHERE id=$1`, id); err != nil {
			return PersonSummary{}, err
		}
		if err = recordPeopleAudit(ctx, tx, actorID, "people.membership_updated", "membership", id.String(), organizationID, accountID, revision, revision+1, "success", map[string]any{auditStatusField: current}, map[string]any{auditStatusField: status}); err != nil {
			return PersonSummary{}, err
		}
	}
	return getPersonInTransaction(ctx, tx, organizationID, accountID)
}

func (s *Service) UpdateProfileGroup(ctx context.Context, organizationID uuid.UUID, actorID, accountID int, profileID string, expectedRevision int64, groupID int) (PersonSummary, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PersonSummary{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	person, err := s.UpdateProfileGroupInTransaction(ctx, tx, organizationID, actorID, accountID, profileID, expectedRevision, groupID)
	if err != nil {
		return PersonSummary{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PersonSummary{}, err
	}
	return person, nil
}

func (s *Service) UpdateProfileGroupInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actorID, accountID int, profileID string, expectedRevision int64, groupID int) (PersonSummary, error) {
	if groupID <= 0 || strings.TrimSpace(profileID) == "" {
		return PersonSummary{}, ErrInvalidBulkAction
	}
	var err error
	var groupExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM access_groups WHERE organization_id=$1 AND id=$2)`, organizationID, groupID).Scan(&groupExists); err != nil {
		return PersonSummary{}, err
	}
	if !groupExists {
		return PersonSummary{}, ErrNotFound
	}
	var membershipID uuid.UUID
	var revision int64
	if err = tx.QueryRow(ctx, `SELECT id,security_revision FROM organization_memberships WHERE organization_id=$1 AND account_id=$2 FOR UPDATE`, organizationID, accountID).Scan(&membershipID, &revision); errors.Is(err, pgx.ErrNoRows) {
		return PersonSummary{}, ErrNotFound
	} else if err != nil {
		return PersonSummary{}, err
	}
	if revision != expectedRevision {
		return PersonSummary{}, ErrAuthorizationStateChanged
	}
	var beforeGroup int
	err = tx.QueryRow(ctx, `SELECT access_group_id FROM user_profiles WHERE organization_id=$1 AND user_id=$2 AND id=$3 FOR UPDATE`, organizationID, accountID, profileID).Scan(&beforeGroup)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonSummary{}, ErrNotFound
	}
	if err != nil {
		return PersonSummary{}, err
	}
	if beforeGroup != groupID {
		if _, err = tx.Exec(ctx, `UPDATE user_profiles SET access_group_id=$4,updated_at=now() WHERE organization_id=$1 AND user_id=$2 AND id=$3`, organizationID, accountID, profileID, groupID); err != nil {
			return PersonSummary{}, err
		}
		if err = bumpPersonRevisions(ctx, tx, membershipID, accountID); err != nil {
			return PersonSummary{}, err
		}
		if err = recordPeopleAudit(ctx, tx, actorID, "people.profile_group_updated", "profile", profileID, organizationID, accountID, revision, revision+1, "success", map[string]any{"group_id": beforeGroup}, map[string]any{"group_id": groupID}); err != nil {
			return PersonSummary{}, err
		}
	}
	return getPersonInTransaction(ctx, tx, organizationID, accountID)
}

func getPersonInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int) (PersonSummary, error) {
	var person PersonSummary
	err := tx.QueryRow(ctx, `SELECT m.organization_id,m.account_id,COALESCE(u.email,''),COALESCE(NULLIF(u.username,''),u.email,''),m.id,m.status,m.legacy_role,m.security_revision,GREATEST(m.updated_at,COALESCE((SELECT max(p.updated_at) FROM user_profiles p WHERE p.organization_id=m.organization_id AND p.user_id=m.account_id),m.updated_at)) FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE m.organization_id=$1 AND m.account_id=$2`, organizationID, accountID).Scan(&person.OrganizationID, &person.AccountID, &person.Email, &person.DisplayName, &person.MembershipID, &person.MembershipStatus, &person.LegacyRole, &person.SecurityRevision, &person.LastActivity)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonSummary{}, ErrNotFound
	}
	if err != nil {
		return PersonSummary{}, err
	}
	person.Profiles = []ProfileSummary{}
	rows, err := tx.Query(ctx, `SELECT p.id,p.name,p.access_group_id,g.name,p.updated_at FROM user_profiles p JOIN access_groups g ON g.organization_id=p.organization_id AND g.id=p.access_group_id WHERE p.organization_id=$1 AND p.user_id=$2 ORDER BY lower(p.name),p.id`, organizationID, accountID)
	if err != nil {
		return PersonSummary{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var profile ProfileSummary
		if err := rows.Scan(&profile.ID, &profile.Name, &profile.GroupID, &profile.GroupName, &profile.UpdatedAt); err != nil {
			return PersonSummary{}, err
		}
		person.Profiles = append(person.Profiles, profile)
	}
	return person, rows.Err()
}
