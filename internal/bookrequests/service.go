package bookrequests

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Notifier interface {
	Lifecycle(context.Context, Request, string)
	Fulfilled(context.Context, Request) error
}

type Service struct {
	pool                 *pgxpool.Pool
	notifier             Notifier
	ebookFulfillment     Fulfillment
	audiobookFulfillment AudiobookFulfillment
	// audiobookNow overrides the run-budget clock in tests.
	audiobookNow func() time.Time
}

func NewService(pool *pgxpool.Pool, notifier Notifier) *Service {
	return &Service{pool: pool, notifier: notifier}
}

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type policy struct {
	Capability
	start time.Time
}

// Policy reads the actual profile group and organization membership, never the
// account's default-organization projection. Book quotas have independent counters.
func readPolicy(ctx context.Context, q querier, v Viewer) (policy, error) {
	return readPolicyFor(ctx, q, v, "audiobook")
}

func readPolicyFor(ctx context.Context, q querier, v Viewer, mediaType string) (policy, error) {
	p := policy{Capability: Capability{Available: true, MediaTypes: []string{mediaType}, FulfilmentBackend: "unconfigured", ManualFulfilment: true}}
	if v.UserID <= 0 || v.ProfileID == "" || v.OrganizationID == uuid.Nil {
		return p, ErrForbidden
	}
	var allowed bool
	var limitMode, approval string
	var max, days *int
	err := q.QueryRow(ctx, `
 SELECT s.requests_enabled, s.global_max_requests, s.global_window_days, s.global_auto_approval_enabled,
   COALESCE(u.requests_allowed, CASE WHEN m.legacy_role='admin' THEN true ELSE COALESCE(g.requests_allowed,true) END),
   COALESCE(NULLIF(ul.limit_mode,'inherit'), CASE WHEN m.legacy_role<>'admin' THEN NULLIF(gl.limit_mode,'inherit') END,'inherit'),
   CASE WHEN COALESCE(ul.limit_mode,'inherit')<>'inherit' THEN ul.max_requests WHEN m.legacy_role<>'admin' THEN gl.max_requests END,
   CASE WHEN COALESCE(ul.limit_mode,'inherit')<>'inherit' THEN ul.window_days WHEN m.legacy_role<>'admin' THEN gl.window_days END,
   COALESCE(NULLIF(ul.approval_mode,'inherit'), CASE WHEN m.legacy_role<>'admin' THEN NULLIF(gl.approval_mode,'inherit') END,'inherit')
 FROM user_profiles p
 JOIN users u ON u.id=p.user_id AND u.enabled
 JOIN organization_memberships m ON m.account_id=p.user_id AND m.organization_id=p.organization_id AND m.status='active'
 JOIN organizations o ON o.id=m.organization_id AND (o.status='active' OR (o.is_default AND o.status='initializing'))
 CROSS JOIN request_settings s
 LEFT JOIN access_groups g ON g.id=p.access_group_id AND g.organization_id=p.organization_id
 LEFT JOIN request_group_limits gl ON gl.group_id=g.id
 LEFT JOIN request_user_limits ul ON ul.user_id=p.user_id
 WHERE p.user_id=$1 AND p.id=$2 AND p.organization_id=$3 AND s.id=true
 FOR SHARE OF p,m,u,o,s`, v.UserID, v.ProfileID, v.OrganizationID).Scan(&p.Enabled, &p.Limit, &p.WindowDays, &p.AutoApprove, &allowed, &limitMode, &max, &days, &approval)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrForbidden
	}
	if err != nil {
		return p, err
	}
	p.CanRequest = p.Enabled && allowed
	switch limitMode {
	case "blocked":
		p.CanRequest = false
	case "unlimited":
		p.Unlimited = true
	case "custom":
		if max != nil {
			p.Limit = *max
		}
		if days != nil {
			p.WindowDays = *days
		}
	}
	switch approval {
	case "blocked":
		p.CanRequest = false
	case "auto":
		p.AutoApprove = true
	case "manual":
		p.AutoApprove = false
	}
	if p.WindowDays <= 0 {
		p.WindowDays = 7
	}
	p.start = time.Now().UTC().AddDate(0, 0, -p.WindowDays)
	if err = q.QueryRow(ctx, `SELECT count(*) FROM bloem_book_requests WHERE requested_by_user_id=$1 AND created_at >= $2 AND outcome<>'declined' AND media_type=$3`, v.UserID, p.start, mediaType).Scan(&p.Used); err != nil {
		return p, err
	}
	p.Remaining = maxInt(0, p.Limit-p.Used)
	return p, nil
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func (s *Service) Capability(ctx context.Context, v Viewer) (Capability, error) {
	p, e := readPolicy(ctx, s.pool, v)
	if e != nil || s.audiobookFulfillment == nil {
		return p.Capability, e
	}
	available, e := AudiobookAcquisitionAvailable(ctx, s.pool, v.OrganizationID)
	if available {
		p.FulfilmentBackend = "audiobook_acquisition"
	}
	return p.Capability, e
}

const selectColumns = `id,organization_id,media_type,title,author,isbn,asin,note,status,outcome,outcome_reason,requested_by_user_id,requested_by_profile_id,library_content_id,fulfilment,revision,created_at,updated_at,completed_at`

func scan(row pgx.Row) (Request, error) {
	var r Request
	err := row.Scan(&r.ID, &r.OrganizationID, &r.MediaType, &r.Title, &r.Author, &r.ISBN, &r.ASIN, &r.Note, &r.Status, &r.Outcome, &r.OutcomeReason, &r.RequestedByUserID, &r.RequestedByProfileID, &r.LibraryContentID, &r.Fulfilment, &r.Revision, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}
func (s *Service) Get(ctx context.Context, v Viewer, id string) (Request, error) {
	return getRequest(ctx, s.pool, v, id, "audiobook")
}

func getRequest(ctx context.Context, q querier, v Viewer, id, mediaType string) (Request, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Request{}, ErrNotFound
	}
	r, err := scan(q.QueryRow(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE id=$1 AND organization_id=$2 AND ($3 OR (requested_by_user_id=$4 AND requested_by_profile_id=$5)) AND media_type=$6`, id, v.OrganizationID, v.Admin, v.UserID, v.ProfileID, mediaType))
	if err != nil {
		return r, err
	}
	if !canRead(v, r) {
		return Request{}, ErrNotFound
	}
	return r, nil
}
func (s *Service) List(ctx context.Context, v Viewer, limit, offset int) ([]Request, error) {
	if v.UserID <= 0 || v.OrganizationID == uuid.Nil || !v.Admin && v.ProfileID == "" {
		return nil, ErrForbidden
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	if offset < 0 {
		return nil, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE organization_id=$1 AND media_type='audiobook' AND ($2 OR (requested_by_user_id=$3 AND requested_by_profile_id=$4)) ORDER BY created_at DESC,id DESC LIMIT $5 OFFSET $6`, v.OrganizationID, v.Admin, v.UserID, v.ProfileID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) Create(ctx context.Context, v Viewer, in CreateInput, key string) (Request, bool, error) {
	in, err := normalizeInput(in)
	if err != nil {
		return Request{}, false, err
	}
	return s.create(ctx, v, in, key, "audiobook")
}

func (s *Service) create(ctx context.Context, v Viewer, in CreateInput, key, mediaType string) (Request, bool, error) {
	if len(key) < 16 || len(key) > 128 || strings.ContainsAny(key, "\r\n\t ") {
		return Request{}, false, fmt.Errorf("%w: provide an Idempotency-Key of 16 to 128 characters", ErrInvalid)
	}
	encoded, _ := json.Marshal(in)
	digest := fmt.Sprintf("%x", sha256.Sum256(encoded))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, false, err
	}
	defer tx.Rollback(ctx)
	// Independent book-lane quota lock. Every filing by an account serializes,
	// including unlimited callers, so policy and key retries remain consistent.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(143,$1::int4)`, v.UserID); err != nil {
		return Request{}, false, err
	}
	if mediaType == "book" {
		if err = checkEbookAuthority(ctx, tx, v, true); err != nil {
			return Request{}, false, err
		}
	}
	p, err := readPolicyFor(ctx, tx, v, mediaType)
	if err != nil {
		return Request{}, false, err
	}
	if !p.Enabled {
		return Request{}, false, ErrDisabled
	}
	if !p.CanRequest {
		return Request{}, false, ErrForbidden
	}
	var previousDigest string
	var previousMediaType string
	err = tx.QueryRow(ctx, `SELECT request_digest,media_type FROM bloem_book_requests WHERE organization_id=$1 AND requested_by_user_id=$2 AND requested_by_profile_id=$3 AND client_key=$4`, v.OrganizationID, v.UserID, v.ProfileID, key).Scan(&previousDigest, &previousMediaType)
	if err == nil {
		if previousDigest != digest || previousMediaType != mediaType {
			return Request{}, false, ErrConflict
		}
		r, e := scan(tx.QueryRow(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE organization_id=$1 AND requested_by_user_id=$2 AND requested_by_profile_id=$3 AND client_key=$4`, v.OrganizationID, v.UserID, v.ProfileID, key))
		return r, false, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Request{}, false, err
	}
	if !p.Unlimited && p.Used >= p.Limit {
		return Request{}, false, ErrQuota
	}
	status := "pending"
	if p.AutoApprove {
		status = "approved"
	}
	r, err := scan(tx.QueryRow(ctx, `INSERT INTO bloem_book_requests(id,organization_id,identity_key,title,author,isbn,asin,note,status,requested_by_user_id,requested_by_profile_id,client_key,request_digest,media_type) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING `+selectColumns, uuid.New(), v.OrganizationID, in.identity, in.Title, in.Author, in.ISBN, in.ASIN, in.Note, status, v.UserID, v.ProfileID, key, digest, mediaType))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return Request{}, false, ErrDuplicate
	}
	if err != nil {
		return Request{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Request{}, false, err
	}
	if s.notifier != nil {
		s.notifier.Lifecycle(ctx, r, "submitted")
		if status == "approved" {
			s.notifier.Lifecycle(ctx, r, "auto_approved")
		}
	}
	return r, true, nil
}

func (s *Service) Act(ctx context.Context, v Viewer, id, action string, in ActionInput) (Request, error) {
	return s.act(ctx, v, id, action, in, "audiobook")
}

func (s *Service) act(ctx context.Context, v Viewer, id, action string, in ActionInput, mediaType string) (Request, error) {
	if in.ExpectedRevision <= 0 || len(in.Reason) > 2000 {
		return Request{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback(ctx)
	r, err := scan(tx.QueryRow(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE id=$1 AND organization_id=$2 AND media_type=$3 FOR UPDATE`, id, v.OrganizationID, mediaType))
	if err != nil {
		return Request{}, err
	}
	if !canRead(v, r) {
		return Request{}, ErrNotFound
	}
	if action != "cancel" && !v.Admin {
		return Request{}, ErrForbidden
	}
	if mediaType == "book" {
		if err = checkEbookAuthority(ctx, tx, v, action == "approve" || action == "retry"); err != nil {
			return Request{}, err
		}
	}
	if r.Revision != in.ExpectedRevision {
		return Request{}, ErrConflict
	}
	if r.Outcome != "active" || r.Status == "completed" {
		return Request{}, ErrConflict
	}
	var audiobookReason string
	switch action {
	case "approve":
		if r.Status != "pending" {
			return Request{}, ErrConflict
		}
		r.Status = "approved"
	case "decline":
		if r.Status != "pending" {
			return Request{}, ErrConflict
		}
		r.Outcome = "declined"
	case "cancel":
		r.Outcome = "cancelled"
		if mediaType == "book" {
			if _, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET lease_token=NULL,lease_until=NULL WHERE request_id=$1`, r.ID); err != nil {
				return Request{}, err
			}
		}
	case "retry":
		if r.Status != "approved" {
			return Request{}, ErrConflict
		}
		if mediaType == "audiobook" {
			// Every audiobook action leaves the dispatch lease untouched.
			if r.Fulfilment, audiobookReason, err = s.retryAudiobookTx(ctx, tx, r); err != nil {
				return Request{}, err
			}
			break
		}
		if mediaType != "book" || r.Fulfilment != "failed" && r.Fulfilment != "retrying" {
			return Request{}, ErrConflict
		}
		if err = s.retryEbookTx(ctx, tx, r); err != nil {
			return Request{}, err
		}
		r.Fulfilment = "retrying"
	case "fulfil":
		r.Status = "completed"
		r.Fulfilment = "manual"
	default:
		return Request{}, ErrInvalid
	}
	reason := strings.TrimSpace(in.Reason)
	if mediaType == "book" && action == "retry" {
		reason = "Checking the existing submission without submitting again."
	}
	if mediaType == "audiobook" && action == "retry" {
		reason = audiobookReason
	}
	r, err = scan(tx.QueryRow(ctx, `UPDATE bloem_book_requests SET status=$2,outcome=$3,outcome_reason=$4,fulfilment=$5,completed_at=CASE WHEN $2='completed' THEN now() ELSE NULL END,revision=revision+1,updated_at=now() WHERE id=$1 RETURNING `+selectColumns, id, r.Status, r.Outcome, reason, r.Fulfilment))
	if err != nil {
		return Request{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Request{}, err
	}
	if s.notifier != nil && action != "fulfil" {
		s.notifier.Lifecycle(ctx, r, action)
	}
	// Fulfilled deliveries are retried by the durable reconciliation task.
	return r, nil
}
