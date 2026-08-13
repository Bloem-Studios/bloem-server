package policy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrDecisionNotFound is returned when a policy decision row cannot be found.
var ErrDecisionNotFound = errors.New("policy decision not found")

// ListOptions controls policy decision log filtering and pagination.
type ListOptions struct {
	DecisionName string
	UserID       *int
	Allowed      *bool
	From         *time.Time
	To           *time.Time
	Limit        int
	Cursor       string
}

// ListResult is one page of policy decision log rows.
type ListResult struct {
	Entries    []Entry `json:"entries"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

// DecisionRepository queries policy decision log rows.
type DecisionRepository struct {
	pool *pgxpool.Pool
}

// NewDecisionRepository creates a policy decision log query repository.
func NewDecisionRepository(pool *pgxpool.Pool) *DecisionRepository {
	return &DecisionRepository{pool: pool}
}

// List returns a cursor-paginated page ordered newest-first.
func (r *DecisionRepository) List(ctx context.Context, opts ListOptions) (ListResult, error) {
	return r.list(ctx, nil, opts)
}

// ListForOrganization lists decisions from one authoritative tenant boundary.
func (r *DecisionRepository) ListForOrganization(ctx context.Context, organizationID uuid.UUID, opts ListOptions) (ListResult, error) {
	if organizationID == uuid.Nil {
		return ListResult{}, ErrDecisionNotFound
	}
	return r.list(ctx, &organizationID, opts)
}

func (r *DecisionRepository) list(ctx context.Context, organizationID *uuid.UUID, opts ListOptions) (ListResult, error) {
	limit := opts.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	conditions := []string{"1=1"}
	args := make([]any, 0, 8)
	argIdx := 1
	if organizationID != nil {
		conditions = append(conditions, fmt.Sprintf("organization_id = $%d", argIdx))
		args = append(args, *organizationID)
		argIdx++
	}

	if opts.From != nil {
		conditions = append(conditions, fmt.Sprintf(`"timestamp" >= $%d`, argIdx))
		args = append(args, *opts.From)
		argIdx++
	}
	if opts.To != nil {
		conditions = append(conditions, fmt.Sprintf(`"timestamp" <= $%d`, argIdx))
		args = append(args, *opts.To)
		argIdx++
	}
	if opts.DecisionName != "" {
		conditions = append(conditions, fmt.Sprintf("decision_name = $%d", argIdx))
		args = append(args, opts.DecisionName)
		argIdx++
	}
	if opts.UserID != nil {
		conditions = append(conditions, fmt.Sprintf("user_id = $%d", argIdx))
		args = append(args, *opts.UserID)
		argIdx++
	}
	if opts.Allowed != nil {
		conditions = append(conditions, fmt.Sprintf("allowed = $%d", argIdx))
		args = append(args, *opts.Allowed)
		argIdx++
	}
	if opts.Cursor != "" {
		cursorTs, cursorID, err := decodeDecisionCursor(opts.Cursor)
		if err != nil {
			return ListResult{}, err
		}
		conditions = append(conditions, fmt.Sprintf(`("timestamp", id) < ($%d, $%d)`, argIdx, argIdx+1))
		args = append(args, cursorTs, cursorID)
		argIdx += 2
	}

	query := fmt.Sprintf(`
		SELECT id, "timestamp", organization_id, membership_id, decision_name, policy_generation, user_id,
		       COALESCE(profile_id, ''), COALESCE(session_id, ''), COALESCE(request_id, ''),
		       COALESCE(node_id, ''), allowed, eval_time_ns, input_digest,
		       input_sample, result_sample, COALESCE(error, '')
		FROM policy_decisions
		WHERE %s
		ORDER BY "timestamp" DESC, id DESC
		LIMIT $%d
	`, strings.Join(conditions, " AND "), argIdx)
	args = append(args, limit+1)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return ListResult{}, fmt.Errorf("list policy decisions: %w", err)
	}
	defer rows.Close()

	entries := make([]Entry, 0, limit+1)
	for rows.Next() {
		entry, err := scanDecisionEntry(rows)
		if err != nil {
			return ListResult{}, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, fmt.Errorf("iterate policy decisions: %w", err)
	}

	result := ListResult{}
	if len(entries) > limit {
		last := entries[limit-1]
		result.NextCursor = encodeDecisionCursor(last.Timestamp, last.ID)
		entries = entries[:limit]
	}
	result.Entries = entries
	return result, nil
}

// Get returns one policy decision row. When timestamp is nil, the newest row
// with the id is returned; callers that need the partition primary key can pass
// both id and timestamp.
func (r *DecisionRepository) Get(ctx context.Context, id int64, timestamp *time.Time) (Entry, error) {
	return r.get(ctx, nil, id, timestamp)
}

// GetForOrganization returns a decision only when it belongs to the selected
// organization. A foreign identifier is indistinguishable from a missing one.
func (r *DecisionRepository) GetForOrganization(ctx context.Context, organizationID uuid.UUID, id int64, timestamp *time.Time) (Entry, error) {
	if organizationID == uuid.Nil {
		return Entry{}, ErrDecisionNotFound
	}
	return r.get(ctx, &organizationID, id, timestamp)
}

func (r *DecisionRepository) get(ctx context.Context, organizationID *uuid.UUID, id int64, timestamp *time.Time) (Entry, error) {
	query := `
		SELECT id, "timestamp", organization_id, membership_id, decision_name, policy_generation, user_id,
		       COALESCE(profile_id, ''), COALESCE(session_id, ''), COALESCE(request_id, ''),
		       COALESCE(node_id, ''), allowed, eval_time_ns, input_digest,
		       input_sample, result_sample, COALESCE(error, '')
		FROM policy_decisions
		WHERE id = $1
		ORDER BY "timestamp" DESC
		LIMIT 1
	`
	args := []any{id}
	conditions := "id = $1"
	if organizationID != nil {
		args = append(args, *organizationID)
		conditions += " AND organization_id = $2"
	}
	if timestamp != nil {
		args = append(args, *timestamp)
		conditions += fmt.Sprintf(` AND "timestamp" = $%d`, len(args))
		query = `
			SELECT id, "timestamp", organization_id, membership_id, decision_name, policy_generation, user_id,
			       COALESCE(profile_id, ''), COALESCE(session_id, ''), COALESCE(request_id, ''),
			       COALESCE(node_id, ''), allowed, eval_time_ns, input_digest,
			       input_sample, result_sample, COALESCE(error, '')
			FROM policy_decisions
			WHERE ` + conditions + `
		`
	} else if organizationID != nil {
		query = `
			SELECT id, "timestamp", organization_id, membership_id, decision_name, policy_generation, user_id,
			       COALESCE(profile_id, ''), COALESCE(session_id, ''), COALESCE(request_id, ''),
			       COALESCE(node_id, ''), allowed, eval_time_ns, input_digest,
			       input_sample, result_sample, COALESCE(error, '')
			FROM policy_decisions WHERE ` + conditions + ` ORDER BY "timestamp" DESC LIMIT 1`
	}

	entry, err := scanDecisionEntry(r.pool.QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, ErrDecisionNotFound
		}
		return Entry{}, err
	}
	return entry, nil
}

type decisionEntryScanner interface {
	Scan(dest ...any) error
}

func scanDecisionEntry(scanner decisionEntryScanner) (Entry, error) {
	var entry Entry
	var decisionName string
	var inputSample []byte
	var resultSample []byte
	var membershipID *uuid.UUID
	if err := scanner.Scan(
		&entry.ID,
		&entry.Timestamp,
		&entry.OrganizationID,
		&membershipID,
		&decisionName,
		&entry.PolicyGeneration,
		&entry.UserID,
		&entry.ProfileID,
		&entry.SessionID,
		&entry.RequestID,
		&entry.NodeID,
		&entry.Allowed,
		&entry.EvalTimeNS,
		&entry.InputDigest,
		&inputSample,
		&resultSample,
		&entry.Error,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, err
		}
		return Entry{}, fmt.Errorf("scan policy decision row: %w", err)
	}
	entry.DecisionName = DecisionName(decisionName)
	if membershipID != nil {
		entry.MembershipID = *membershipID
	}
	if len(inputSample) > 0 {
		entry.InputSample = append(json.RawMessage(nil), inputSample...)
	}
	if len(resultSample) > 0 {
		entry.ResultSample = append(json.RawMessage(nil), resultSample...)
	}
	return entry, nil
}

func encodeDecisionCursor(ts time.Time, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%d", ts.UnixNano(), id)))
}

func decodeDecisionCursor(cursor string) (time.Time, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("decode cursor: %w", err)
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 2 {
		return time.Time{}, 0, fmt.Errorf("invalid cursor")
	}
	var nanos int64
	var id int64
	if _, err := fmt.Sscanf(parts[0], "%d", &nanos); err != nil {
		return time.Time{}, 0, fmt.Errorf("parse cursor timestamp: %w", err)
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &id); err != nil {
		return time.Time{}, 0, fmt.Errorf("parse cursor id: %w", err)
	}
	return time.Unix(0, nanos).UTC(), id, nil
}
