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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/idgen"
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

	AuthorityPlatformAdmin     = "platform_admin"
	AuthorityOrganizationAdmin = "organization_admin"
	maximumSelectionTargets    = 10000
	defaultBulkBatchSize       = 100
)

var (
	ErrNotFound                  = errors.New("organization person resource not found")
	ErrInvalidCursor             = errors.New("invalid people cursor")
	ErrInvalidSelection          = errors.New("invalid immutable selection")
	ErrSelectionExpired          = errors.New("immutable selection expired")
	ErrInvalidFilter             = errors.New("invalid people filter")
	ErrInvalidBulkAction         = errors.New("invalid people bulk action")
	ErrAuthorizationStateChanged = errors.New("authorization state changed")
)

type Filter struct {
	Query       string
	Status      []tenancy.MembershipStatus
	GroupIDs    []int
	ActiveSince *time.Time
	Sort        string
	Limit       int
	Cursor      string
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

type RecordResult struct {
	AccountID int    `json:"account_id"`
	Reason    string `json:"reason"`
}

type BulkResult struct {
	JobID           string         `json:"job_id"`
	Status          string         `json:"status"`
	ProgressCurrent int            `json:"progress_current"`
	ProgressTotal   int            `json:"progress_total"`
	Succeeded       int            `json:"succeeded"`
	Skipped         []RecordResult `json:"skipped"`
	Failed          []RecordResult `json:"failed"`
}

type MutationActor struct {
	AccountID int
	Authority string
	RequestID string
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

type Service struct {
	pool *pgxpool.Pool
	key  [32]byte
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool, secret string) *Service {
	return &Service{pool: pool, key: sha256.Sum256([]byte(secret)), now: time.Now}
}

type canonicalFilter struct {
	Query       string                     `json:"query,omitempty"`
	Status      []tenancy.MembershipStatus `json:"status,omitempty"`
	GroupIDs    []int                      `json:"group_ids,omitempty"`
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
	if filter.ActiveSince != nil {
		active := filter.ActiveSince.UTC()
		value.ActiveSince = &active
	}
	return value
}

func (f canonicalFilter) toFilter() Filter {
	return Filter{Query: f.Query, Status: f.Status, GroupIDs: f.GroupIDs, ActiveSince: f.ActiveSince, Sort: f.Sort}
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
	if err != nil {
		return cursor, ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write([]byte("cursor." + parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursor, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(raw, &cursor) != nil {
		return peopleCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

type profileSnapshot struct {
	ID        string    `json:"id"`
	GroupID   int       `json:"group_id"`
	UpdatedAt time.Time `json:"updated_at"`
}
type targetSnapshot struct {
	AccountID             int                      `json:"account_id"`
	MembershipID          uuid.UUID                `json:"membership_id"`
	MembershipStatus      tenancy.MembershipStatus `json:"membership_status"`
	MembershipRevision    int64                    `json:"membership_revision"`
	AccountPolicyRevision int64                    `json:"account_policy_revision"`
	Profiles              []profileSnapshot        `json:"profiles"`
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
	// The materialized account IDs are the immutable snapshot. snapshot_at is
	// retained for confirmation/audit; adding an application-clock cutoff here
	// would incorrectly exclude rows when database and application clocks skew.
	conditions, args := buildPeopleConditions(organizationID, canonical.toFilter(), nil)
	rows, err := s.pool.Query(ctx, `
		SELECT m.account_id,m.id,m.status,m.security_revision,u.access_policy_revision,
		       COALESCE((SELECT jsonb_agg(jsonb_build_object('id',p.id,'group_id',p.access_group_id,'updated_at',p.updated_at) ORDER BY p.id) FROM user_profiles p WHERE p.organization_id=m.organization_id AND p.user_id=m.account_id),'[]'::jsonb)
		FROM organization_memberships m JOIN users u ON u.id=m.account_id
		WHERE `+strings.Join(conditions, " AND ")+` ORDER BY m.account_id LIMIT `+strconv.Itoa(maximumSelectionTargets+1), args...)
	if err != nil {
		return Selection{}, fmt.Errorf("snapshot people selection: %w", err)
	}
	ids := []int{}
	targets := []targetSnapshot{}
	for rows.Next() {
		var target targetSnapshot
		var profilesJSON []byte
		if err := rows.Scan(&target.AccountID, &target.MembershipID, &target.MembershipStatus, &target.MembershipRevision, &target.AccountPolicyRevision, &profilesJSON); err != nil {
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
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE `+strings.Join(conditions, " AND "), args...).Scan(&total); err != nil {
		return Selection{}, fmt.Errorf("count people selection: %w", err)
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
	if _, err = s.pool.Exec(ctx, `INSERT INTO admin_people_selections (id,organization_id,canonical_filter,snapshot_at,account_ids,matched_count,excluded_count,expires_at,targets) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, reference, organizationID, filterJSON, snapshot, ids, len(ids), excluded, expires, targetsJSON); err != nil {
		return Selection{}, fmt.Errorf("persist immutable people selection: %w", err)
	}
	token, err := s.signSelectionReference(reference)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Token: token, Matched: int64(len(ids)), Excluded: excluded, ExpiresAt: expires}, nil
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
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return uuid.Nil, ErrInvalidSelection
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) != 16 {
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
	tag, err := s.pool.Exec(ctx, `DELETE FROM admin_people_selections WHERE id IN (SELECT id FROM admin_people_selections WHERE expires_at<=now() AND NOT EXISTS (SELECT 1 FROM admin_people_bulk_jobs j WHERE j.selection_id=admin_people_selections.id) ORDER BY expires_at LIMIT $1)`, limit)
	if err != nil {
		return 0, fmt.Errorf("cleanup expired people selections: %w", err)
	}
	return tag.RowsAffected(), nil
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
	if validateBulkAction(action) != nil || s == nil || s.pool == nil || organizationID == uuid.Nil || actorID <= 0 {
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BulkResult{}, fmt.Errorf("begin people bulk enqueue: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	record, err := loadSelection(ctx, tx.QueryRow(ctx, `SELECT id,organization_id,canonical_filter,snapshot_at,expires_at,account_ids,matched_count,excluded_count,targets FROM admin_people_selections WHERE id=$1 AND organization_id=$2`, reference, organizationID), organizationID)
	if err != nil {
		return BulkResult{}, err
	}
	if !s.now().UTC().Before(record.expires) {
		return BulkResult{}, ErrSelectionExpired
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
	actionKey := action.Kind + ":"
	if action.GroupID != nil {
		actionKey += strconv.Itoa(*action.GroupID)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, reference.String()+":"+actionKey); err != nil {
		return BulkResult{}, fmt.Errorf("lock people bulk idempotency key: %w", err)
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT job_id FROM admin_people_bulk_jobs WHERE selection_id=$1 AND action_key=$2`, reference, actionKey).Scan(&existing)
	if err == nil {
		_ = tx.Commit(ctx)
		return s.GetBulkJob(ctx, organizationID, existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BulkResult{}, err
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
	if _, err = tx.Exec(ctx, `INSERT INTO admin_people_bulk_jobs (job_id,organization_id,selection_id,action_kind,group_id,action_key,actor_account_id,actor_authority,request_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''))`, jobID, organizationID, reference, action.Kind, action.GroupID, actionKey, actorID, actor.Authority, actor.RequestID); err != nil {
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
	if err = tx.Commit(ctx); err != nil {
		return BulkResult{}, err
	}
	return initial, nil
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
		if result.Status == "completed" || result.Status == "failed" {
			return result, nil
		}
	}
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
	var actor MutationActor
	var status string
	err = tx.QueryRow(ctx, `SELECT j.status,b.action_kind,b.group_id,b.actor_account_id,b.actor_authority,COALESCE(b.request_id,'') FROM admin_jobs j JOIN admin_people_bulk_jobs b ON b.job_id=j.id WHERE j.id=$1 AND b.organization_id=$2 FOR UPDATE OF j`, jobID, organizationID).Scan(&status, &action.Kind, &action.GroupID, &actor.AccountID, &actor.Authority, &actor.RequestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return BulkResult{}, ErrNotFound
	}
	if err != nil {
		return BulkResult{}, err
	}
	ctx = WithMutationActor(ctx, actor)
	if status == "completed" || status == "failed" {
		_ = tx.Commit(ctx)
		return s.GetBulkJob(ctx, organizationID, jobID)
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
		outcome, reason, mutationErr := s.executeBulkRecord(ctx, tx, organizationID, actor.AccountID, item.snapshot, action)
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
			if err = recordPeopleAudit(ctx, tx, actor.AccountID, "people.bulk_job_completed", "bulk_job", jobID, organizationID, 0, 0, 0, "success", nil, map[string]any{"succeeded": result.Succeeded, "skipped": len(result.Skipped), "failed": len(result.Failed)}); err != nil {
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

func (s *Service) failBulkJob(ctx context.Context, organizationID uuid.UUID, jobID string, cause error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var actor MutationActor
	var status string
	err = tx.QueryRow(ctx, `SELECT j.status,b.actor_account_id,b.actor_authority,COALESCE(b.request_id,'') FROM admin_jobs j JOIN admin_people_bulk_jobs b ON b.job_id=j.id WHERE j.id=$1 AND b.organization_id=$2 FOR UPDATE OF j`, jobID, organizationID).Scan(&status, &actor.AccountID, &actor.Authority, &actor.RequestID)
	if err != nil {
		return err
	}
	if status == "completed" || status == "failed" {
		return tx.Commit(ctx)
	}
	ctx = WithMutationActor(ctx, actor)
	result, _, loadErr := bulkResultInTx(ctx, tx, jobID)
	if loadErr != nil {
		return loadErr
	}
	result.Status = "failed"
	raw, _ := json.Marshal(result)
	if _, err = tx.Exec(ctx, `UPDATE admin_jobs SET status='failed',result_payload=$2,message='People bulk operation failed',error_message=$3,completed_at=now(),heartbeat_at=now(),updated_at=now() WHERE id=$1`, jobID, raw, cause.Error()); err != nil {
		return err
	}
	if err = recordPeopleAudit(ctx, tx, actor.AccountID, "people.bulk_job_failed", "bulk_job", jobID, organizationID, 0, 0, 0, "failure", nil, map[string]any{"error": "executor_failure"}); err != nil {
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
	err := tx.QueryRow(ctx, `SELECT m.id,m.status,m.security_revision,u.access_policy_revision,(o.owner_account_id=m.account_id) FROM organization_memberships m JOIN organizations o ON o.id=m.organization_id JOIN users u ON u.id=m.account_id WHERE m.organization_id=$1 AND m.account_id=$2 FOR UPDATE OF m,u`, organizationID, snapshot.AccountID).Scan(&membershipID, &status, &revision, &accountRevision, &protected)
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
		rows, err := tx.Query(ctx, `SELECT id,access_group_id,updated_at FROM user_profiles WHERE organization_id=$1 AND user_id=$2 ORDER BY id FOR UPDATE`, organizationID, snapshot.AccountID)
		if err != nil {
			return "", "", err
		}
		profiles := []profileSnapshot{}
		for rows.Next() {
			var profile profileSnapshot
			if err := rows.Scan(&profile.ID, &profile.GroupID, &profile.UpdatedAt); err != nil {
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
		if _, err := tx.Exec(ctx, `UPDATE users SET access_policy_revision=access_policy_revision+1,updated_at=now() WHERE id=$1`, snapshot.AccountID); err != nil {
			return "", "", err
		}
		if err := recordPeopleAudit(ctx, tx, actorID, actionName, "membership", membershipID.String(), organizationID, snapshot.AccountID, revision, revision+1, "success", map[string]any{"status": status}, map[string]any{"status": target}); err != nil {
			return "", "", err
		}
		return "succeeded", "", nil
	}
	return "", "", ErrInvalidBulkAction
}

func sameProfileSnapshots(current, want []profileSnapshot) bool {
	if len(current) != len(want) {
		return false
	}
	for i := range current {
		if current[i].ID != want[i].ID || current[i].GroupID != want[i].GroupID || !current[i].UpdatedAt.Equal(want[i].UpdatedAt) {
			return false
		}
	}
	return true
}

func bumpPersonRevisions(ctx context.Context, tx pgx.Tx, membershipID uuid.UUID, accountID int) error {
	if _, err := tx.Exec(ctx, `UPDATE organization_memberships SET security_revision=security_revision+1,updated_at=now() WHERE id=$1`, membershipID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET access_policy_revision=access_policy_revision+1,updated_at=now() WHERE id=$1`, accountID); err != nil {
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
	var status string
	err = tx.QueryRow(ctx, `SELECT j.status FROM admin_jobs j JOIN admin_people_bulk_jobs b ON b.job_id=j.id WHERE j.id=$1 AND b.organization_id=$2`, jobID, organizationID).Scan(&status)
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
	if err = tx.Commit(ctx); err != nil {
		return BulkResult{}, err
	}
	return result, nil
}

func (s *Service) UpdateMembership(ctx context.Context, organizationID uuid.UUID, actorID, accountID int, expectedRevision int64, status tenancy.MembershipStatus) (PersonSummary, error) {
	if status != tenancy.MembershipActive && status != tenancy.MembershipSuspended {
		return PersonSummary{}, ErrInvalidBulkAction
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PersonSummary{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
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
		if _, err = tx.Exec(ctx, `UPDATE users SET access_policy_revision=access_policy_revision+1,updated_at=now() WHERE id=$1`, accountID); err != nil {
			return PersonSummary{}, err
		}
		if err = recordPeopleAudit(ctx, tx, actorID, "people.membership_updated", "membership", id.String(), organizationID, accountID, revision, revision+1, "success", map[string]any{"status": current}, map[string]any{"status": status}); err != nil {
			return PersonSummary{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return PersonSummary{}, err
	}
	return s.Get(ctx, organizationID, accountID)
}

func (s *Service) UpdateProfileGroup(ctx context.Context, organizationID uuid.UUID, actorID, accountID int, profileID string, expectedRevision int64, groupID int) (PersonSummary, error) {
	if groupID <= 0 || strings.TrimSpace(profileID) == "" {
		return PersonSummary{}, ErrInvalidBulkAction
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PersonSummary{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
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
	if err = tx.Commit(ctx); err != nil {
		return PersonSummary{}, err
	}
	return s.Get(ctx, organizationID, accountID)
}
