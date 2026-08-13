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

	ReasonAlreadyApplied = "already_applied"
	ReasonNoProfiles     = "no_profiles"
	ReasonNotFound       = "not_found"
	ReasonProtectedOwner = "protected_owner"
	ReasonMutationFailed = "mutation_failed"
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
	JobID     string         `json:"job_id"`
	Succeeded int            `json:"succeeded"`
	Skipped   []RecordResult `json:"skipped"`
	Failed    []RecordResult `json:"failed"`
}

type mutationMetadata struct{ requestID string }
type mutationMetadataKey struct{}

func WithMutationRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, mutationMetadataKey{}, mutationMetadata{requestID: strings.TrimSpace(requestID)})
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
	OrganizationID uuid.UUID `json:"organization_id"`
	Sort           string    `json:"sort"`
	Key            string    `json:"key,omitempty"`
	Activity       time.Time `json:"activity,omitempty"`
	AccountID      int       `json:"account_id"`
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
		cursor, err := decodePeopleCursor(filter.Cursor)
		if err != nil || cursor.OrganizationID != organizationID || cursor.Sort != filter.Sort || cursor.AccountID <= 0 {
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
		cursor := peopleCursor{OrganizationID: organizationID, Sort: filter.Sort, AccountID: last.person.AccountID}
		switch filter.Sort {
		case SortName:
			cursor.Key = last.nameKey
		case SortEmail:
			cursor.Key = last.emailKey
		case SortRecentActivity:
			cursor.Activity = last.person.LastActivity
		}
		page.NextCursor = encodePeopleCursor(cursor)
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

func encodePeopleCursor(cursor peopleCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodePeopleCursor(value string) (peopleCursor, error) {
	var cursor peopleCursor
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(raw, &cursor) != nil {
		return peopleCursor{}, ErrInvalidCursor
	}
	return cursor, nil
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
	rows, err := s.pool.Query(ctx, `SELECT m.account_id FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE `+strings.Join(conditions, " AND ")+` ORDER BY m.account_id`, args...)
	if err != nil {
		return Selection{}, fmt.Errorf("snapshot people selection: %w", err)
	}
	ids := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return Selection{}, fmt.Errorf("scan people selection: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Selection{}, fmt.Errorf("iterate people selection: %w", err)
	}
	rows.Close()
	reference := uuid.New()
	expires := snapshot.Add(selectionTTL)
	filterJSON, err := json.Marshal(canonical)
	if err != nil {
		return Selection{}, err
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO admin_people_selections (id,organization_id,canonical_filter,snapshot_at,account_ids,matched_count,excluded_count,expires_at) VALUES ($1,$2,$3,$4,$5,$6,0,$7)`, reference, organizationID, filterJSON, snapshot, ids, len(ids), expires); err != nil {
		return Selection{}, fmt.Errorf("persist immutable people selection: %w", err)
	}
	token, err := s.signSelectionReference(reference)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Token: token, Matched: int64(len(ids)), ExpiresAt: expires}, nil
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
	matched, excluded int64
}

func loadSelection(ctx context.Context, row pgx.Row, organizationID uuid.UUID) (selectionRecord, error) {
	var record selectionRecord
	var filterJSON []byte
	err := row.Scan(&record.id, &record.organizationID, &filterJSON, &record.snapshot, &record.expires, &record.accountIDs, &record.matched, &record.excluded)
	if errors.Is(err, pgx.ErrNoRows) {
		return selectionRecord{}, ErrInvalidSelection
	}
	if err != nil {
		return selectionRecord{}, fmt.Errorf("load immutable people selection: %w", err)
	}
	if record.organizationID != organizationID || json.Unmarshal(filterJSON, &record.filter) != nil {
		return selectionRecord{}, ErrInvalidSelection
	}
	return record, nil
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
	if validateBulkAction(action) != nil {
		return BulkResult{}, ErrInvalidBulkAction
	}
	if s == nil || s.pool == nil {
		return BulkResult{}, fmt.Errorf("people store unavailable")
	}
	if organizationID == uuid.Nil || actorID <= 0 {
		return BulkResult{}, ErrInvalidBulkAction
	}
	reference, err := s.parseSelectionReference(action.SelectionToken)
	if err != nil {
		return BulkResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BulkResult{}, fmt.Errorf("begin people bulk job: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	record, err := loadSelection(ctx, tx.QueryRow(ctx, `SELECT id,organization_id,canonical_filter,snapshot_at,expires_at,account_ids,matched_count,excluded_count FROM admin_people_selections WHERE id=$1 AND organization_id=$2`, reference, organizationID), organizationID)
	if err != nil {
		return BulkResult{}, err
	}
	if !s.now().UTC().Before(record.expires) {
		return BulkResult{}, ErrSelectionExpired
	}
	if action.Kind == BulkAssignGroup {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM access_groups WHERE organization_id=$1 AND id=$2)`, organizationID, *action.GroupID).Scan(&exists); err != nil {
			return BulkResult{}, fmt.Errorf("validate bulk access group: %w", err)
		}
		if !exists {
			return BulkResult{}, ErrNotFound
		}
	}
	jobID, err := idgen.NextID()
	if err != nil {
		return BulkResult{}, err
	}
	result = BulkResult{JobID: jobID, Skipped: []RecordResult{}, Failed: []RecordResult{}}
	for index, accountID := range record.accountIDs {
		savepoint := fmt.Sprintf("people_record_%d", index)
		if _, err = tx.Exec(ctx, "SAVEPOINT "+savepoint); err != nil {
			return BulkResult{}, fmt.Errorf("create bulk record savepoint: %w", err)
		}
		outcome, reason, mutationErr := s.executeBulkRecord(ctx, tx, organizationID, actorID, accountID, action)
		if mutationErr != nil {
			_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+savepoint)
			result.Failed = append(result.Failed, RecordResult{AccountID: accountID, Reason: ReasonMutationFailed})
		} else {
			switch outcome {
			case "succeeded":
				result.Succeeded++
			case "skipped":
				result.Skipped = append(result.Skipped, RecordResult{AccountID: accountID, Reason: reason})
			case "failed":
				result.Failed = append(result.Failed, RecordResult{AccountID: accountID, Reason: reason})
			}
		}
		if _, err = tx.Exec(ctx, "RELEASE SAVEPOINT "+savepoint); err != nil {
			return BulkResult{}, fmt.Errorf("release bulk record savepoint: %w", err)
		}
	}
	requestJSON, _ := json.Marshal(map[string]any{"organization_id": organizationID, "selection_id": reference, "kind": action.Kind, "group_id": action.GroupID})
	resultJSON, _ := json.Marshal(result)
	now := s.now().UTC()
	_, err = tx.Exec(ctx, `INSERT INTO admin_jobs (id,job_type,status,created_by_user_id,request_payload,result_payload,message,progress_current,progress_total,requested_at,started_at,completed_at,updated_at) VALUES ($1,'organization_people_bulk','completed',$2,$3,$4,'People bulk operation completed',$5,$6,$7,$7,$7,$7)`, jobID, actorID, requestJSON, resultJSON, len(record.accountIDs), len(record.accountIDs), now)
	if err != nil {
		return BulkResult{}, fmt.Errorf("persist people bulk job: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return BulkResult{}, fmt.Errorf("commit people bulk job: %w", err)
	}
	return result, nil
}

func (s *Service) executeBulkRecord(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actorID, accountID int, action BulkAction) (string, string, error) {
	var membershipID uuid.UUID
	var status tenancy.MembershipStatus
	var revision int64
	var protected bool
	err := tx.QueryRow(ctx, `SELECT m.id,m.status,m.security_revision,(o.owner_account_id=m.account_id) FROM organization_memberships m JOIN organizations o ON o.id=m.organization_id WHERE m.organization_id=$1 AND m.account_id=$2 FOR UPDATE OF m`, organizationID, accountID).Scan(&membershipID, &status, &revision, &protected)
	if errors.Is(err, pgx.ErrNoRows) {
		return "failed", ReasonNotFound, nil
	}
	if err != nil {
		return "", "", err
	}
	switch action.Kind {
	case BulkAssignGroup:
		var total, changed int
		if err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE access_group_id<>$3) FROM user_profiles WHERE organization_id=$1 AND user_id=$2`, organizationID, accountID, *action.GroupID).Scan(&total, &changed); err != nil {
			return "", "", err
		}
		if total == 0 {
			return "skipped", ReasonNoProfiles, nil
		}
		if changed == 0 {
			return "skipped", ReasonAlreadyApplied, nil
		}
		if _, err := tx.Exec(ctx, `UPDATE user_profiles SET access_group_id=$3,updated_at=now() WHERE organization_id=$1 AND user_id=$2 AND access_group_id<>$3`, organizationID, accountID, *action.GroupID); err != nil {
			return "", "", err
		}
		if err := bumpPersonRevisions(ctx, tx, membershipID, accountID); err != nil {
			return "", "", err
		}
		if err := recordPeopleAudit(ctx, tx, actorID, "people.bulk_group_assigned", "membership", membershipID.String(), organizationID, accountID, revision, revision+1, map[string]any{"changed_profiles": changed}, map[string]any{"group_id": *action.GroupID, "changed_profiles": changed}); err != nil {
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
		if _, err := tx.Exec(ctx, `UPDATE users SET access_policy_revision=access_policy_revision+1,updated_at=now() WHERE id=$1`, accountID); err != nil {
			return "", "", err
		}
		if err := recordPeopleAudit(ctx, tx, actorID, actionName, "membership", membershipID.String(), organizationID, accountID, revision, revision+1, map[string]any{"status": status}, map[string]any{"status": target}); err != nil {
			return "", "", err
		}
		return "succeeded", "", nil
	}
	return "", "", ErrInvalidBulkAction
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

func recordPeopleAudit(ctx context.Context, tx pgx.Tx, actorID int, action, targetType, targetID string, organizationID uuid.UUID, accountID int, beforeRevision, afterRevision int64, before, after map[string]any) error {
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	metadata, _ := ctx.Value(mutationMetadataKey{}).(mutationMetadata)
	_, err := tx.Exec(ctx, `INSERT INTO admin_audit_events (actor_account_id,actor_platform_role,authority_context,action,target_type,target_id,organization_id,subject_id,before_revision,after_revision,outcome,request_id,before_state,after_state) VALUES ($1,'organization_admin','organization',$2,$3,$4,$5,$6,$7,$8,'success',NULLIF($9,''),$10,$11)`, actorID, action, targetType, targetID, organizationID, strconv.Itoa(accountID), beforeRevision, afterRevision, metadata.requestID, beforeJSON, afterJSON)
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
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT result_payload FROM admin_jobs WHERE id=$1 AND job_type='organization_people_bulk' AND request_payload->>'organization_id'=$2`, jobID, organizationID.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return BulkResult{}, ErrNotFound
	}
	if err != nil {
		return BulkResult{}, fmt.Errorf("load people bulk job: %w", err)
	}
	var result BulkResult
	if json.Unmarshal(raw, &result) != nil {
		return BulkResult{}, fmt.Errorf("decode people bulk job result")
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
		if err = recordPeopleAudit(ctx, tx, actorID, "people.membership_updated", "membership", id.String(), organizationID, accountID, revision, revision+1, map[string]any{"status": current}, map[string]any{"status": status}); err != nil {
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
		if err = recordPeopleAudit(ctx, tx, actorID, "people.profile_group_updated", "profile", profileID, organizationID, accountID, revision, revision+1, map[string]any{"group_id": beforeGroup}, map[string]any{"group_id": groupID}); err != nil {
			return PersonSummary{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return PersonSummary{}, err
	}
	return s.Get(ctx, organizationID, accountID)
}
