package bookrequests

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AcquisitionDecisionRecord is one stored, validated decision.
type AcquisitionDecisionRecord struct {
	ID        int64               `json:"id"`
	Attempt   int                 `json:"attempt"`
	Phase     string              `json:"phase"`
	Outcome   string              `json:"outcome"`
	CreatedAt time.Time           `json:"created_at"`
	Decision  AcquisitionDecision `json:"decision"`
}

// AudiobookAcquisition is the admin view of one audiobook request's
// acquisition. It is never shown to requesters.
type AudiobookAcquisition struct {
	Request             Request                     `json:"request"`
	Dispatched          bool                        `json:"dispatched"`
	Attempt             int                         `json:"attempt"`
	AttemptStartedAt    *time.Time                  `json:"attempt_started_at,omitempty"`
	ProviderStatus      string                      `json:"provider_status"`
	ExternalStatus      string                      `json:"external_status"`
	ReviewKind          string                      `json:"review_kind,omitempty"`
	SearchPasses        int                         `json:"search_passes"`
	FailureCount        int                         `json:"failure_count"`
	AICalls             int                         `json:"ai_calls"`
	NextAttemptAt       *time.Time                  `json:"next_attempt_at,omitempty"`
	ProviderCompletedAt *time.Time                  `json:"provider_completed_at,omitempty"`
	ApprovedCandidate   string                      `json:"approved_candidate,omitempty"`
	Stuck               bool                        `json:"stuck"`
	StuckReason         string                      `json:"stuck_reason,omitempty"`
	Marker              string                      `json:"marker,omitempty"`
	IntegrationID       string                      `json:"integration_id,omitempty"`
	IntegrationName     string                      `json:"integration_name,omitempty"`
	LibraryID           int                         `json:"library_id,omitempty"`
	DownloadRoots       []string                    `json:"download_roots,omitempty"`
	Decisions           []AcquisitionDecisionRecord `json:"decisions,omitempty"`
}

type AudiobookAcquisitionList struct {
	Acquisitions []AudiobookAcquisition `json:"acquisitions"`
	NextCursor   string                 `json:"next_cursor,omitempty"`
}

type AudiobookCandidateApproval struct {
	ExpectedRevision int64  `json:"expected_revision"`
	CandidateRef     string `json:"candidate_ref"`
}

// audiobookStuckReason classifies stuck acquisitions (A.12). Nothing is
// failed automatically for being slow; an admin retry starts a new attempt.
// A download client the backend has not been able to read for an hour is
// also 'unreachable'; that request keeps waiting rather than failing.
const audiobookStuckReason = `CASE
 WHEN d.request_id IS NULL THEN CASE WHEN r.status='approved' AND r.outcome='active' AND r.updated_at<now()-interval '1 hour' THEN 'not_reached' ELSE '' END
 WHEN r.status<>'approved' OR r.outcome<>'active' THEN ''
 WHEN d.provider_status IN ('queued','downloading') AND a.attempt_started_at<now()-interval '72 hours' THEN 'slow'
 WHEN a.external_status='stalled' AND d.provider_status IN ('queued','downloading') THEN 'stalled'
 WHEN d.provider_status='unknown' AND d.failure_count>=3 THEN 'unreachable'
 WHEN d.provider_status='completed' AND a.provider_completed_at<now()-interval '24 hours' THEN 'not_in_library'
 WHEN d.provider_status NOT IN ('review','failed') AND d.next_attempt_at<now()-interval '1 hour' THEN 'not_reached'
 WHEN a.external_status='unknown' AND d.provider_status IN ('queued','downloading','unknown') AND a.updated_at<now()-interval '1 hour' THEN 'unreachable'
 ELSE '' END`

const audiobookAcquisitionColumns = `r.id,d.request_id IS NOT NULL,COALESCE(a.attempt,0),a.attempt_started_at,COALESCE(d.provider_status,''),COALESCE(a.external_status,''),COALESCE(a.review_kind,''),
 COALESCE(a.search_passes,0),COALESCE(d.failure_count,0),COALESCE(a.ai_calls,0),d.next_attempt_at,a.provider_completed_at,COALESCE(a.approved_candidate,''),
 COALESCE(d.submission_request_id::text,''),d.descriptor,` + audiobookStuckReason

const audiobookAcquisitionFrom = ` FROM bloem_book_requests r
 LEFT JOIN bloem_ebook_request_dispatches d ON d.request_id=r.id
 LEFT JOIN bloem_audiobook_request_acquisitions a ON a.request_id=d.request_id`

func scanAudiobookAcquisition(row pgx.Row) (AudiobookAcquisition, string, error) {
	var out AudiobookAcquisition
	var id, submission string
	var descriptor []byte
	err := row.Scan(&id, &out.Dispatched, &out.Attempt, &out.AttemptStartedAt, &out.ProviderStatus, &out.ExternalStatus, &out.ReviewKind,
		&out.SearchPasses, &out.FailureCount, &out.AICalls, &out.NextAttemptAt, &out.ProviderCompletedAt, &out.ApprovedCandidate,
		&submission, &descriptor, &out.StuckReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, "", ErrNotFound
	}
	if err != nil {
		return out, "", err
	}
	out.Stuck = out.StuckReason != ""
	if out.Dispatched && submission != "" && out.Attempt > 0 {
		out.Marker = AudiobookMarker(submission, out.Attempt)
		var frozen AudiobookIntegration
		if json.Unmarshal(descriptor, &frozen) == nil {
			out.IntegrationID, out.LibraryID, out.DownloadRoots = frozen.ID, frozen.LibraryID, frozen.DownloadRoots
		}
	}
	return out, id, nil
}

func audiobookAdminTx(ctx context.Context, s *Service, v Viewer) (pgx.Tx, error) {
	if !v.Admin {
		return nil, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if err = checkAudiobookAuthority(ctx, tx, v); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

// AudiobookAcquisition returns one request's acquisition with its newest 20
// decisions. Organization-scoped.
func (s *Service) AudiobookAcquisition(ctx context.Context, v Viewer, id string) (AudiobookAcquisition, error) {
	if _, err := uuid.Parse(id); err != nil {
		return AudiobookAcquisition{}, ErrNotFound
	}
	tx, err := audiobookAdminTx(ctx, s, v)
	if err != nil {
		return AudiobookAcquisition{}, err
	}
	defer tx.Rollback(ctx)
	out, _, err := scanAudiobookAcquisition(tx.QueryRow(ctx, `SELECT `+audiobookAcquisitionColumns+audiobookAcquisitionFrom+` WHERE r.id=$1 AND r.organization_id=$2 AND r.media_type='audiobook'`, id, v.OrganizationID))
	if err != nil {
		return out, err
	}
	if out.Request, err = scan(tx.QueryRow(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE id=$1`, id)); err != nil {
		return out, err
	}
	if out.IntegrationID != "" {
		_ = tx.QueryRow(ctx, `SELECT name FROM request_integrations WHERE id=$1`, out.IntegrationID).Scan(&out.IntegrationName)
	}
	rows, err := tx.Query(ctx, `SELECT id,attempt,phase,outcome,created_at,envelope FROM bloem_audiobook_request_decisions WHERE request_id=$1 ORDER BY id DESC LIMIT 20`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Decisions = []AcquisitionDecisionRecord{}
	for rows.Next() {
		var record AcquisitionDecisionRecord
		var envelope []byte
		if err = rows.Scan(&record.ID, &record.Attempt, &record.Phase, &record.Outcome, &record.CreatedAt, &envelope); err != nil {
			return out, err
		}
		if json.Unmarshal(envelope, &record.Decision) != nil || record.Decision.Validate() != nil {
			continue
		}
		out.Decisions = append(out.Decisions, record)
	}
	return out, rows.Err()
}

type audiobookCursor struct {
	UpdatedAt time.Time `json:"u"`
	ID        string    `json:"i"`
}

// ListAudiobookAcquisitions lists the organization's audiobook requests by
// acquisition state, newest change first.
func (s *Service) ListAudiobookAcquisitions(ctx context.Context, v Viewer, state, cursor string, limit int) (AudiobookAcquisitionList, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var filter string
	switch state {
	case "", "all":
		filter = "true"
	case "active":
		filter = `r.outcome='active' AND r.status='approved' AND COALESCE(d.provider_status,'') NOT IN ('review','failed')`
	case "review":
		filter = `r.outcome='active' AND r.status='approved' AND d.provider_status='review'`
	case "failed":
		filter = `r.outcome='active' AND r.status='approved' AND (d.provider_status='failed' OR r.fulfilment='failed')`
	case "stuck":
		filter = `(` + audiobookStuckReason + `)<>''`
	default:
		return AudiobookAcquisitionList{}, fmt.Errorf("%w: state must be active, review, failed, stuck or all", ErrInvalid)
	}
	var after audiobookCursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) > 256 || json.Unmarshal(raw, &after) != nil {
			return AudiobookAcquisitionList{}, fmt.Errorf("%w: invalid cursor", ErrInvalid)
		}
		if _, err = uuid.Parse(after.ID); err != nil {
			return AudiobookAcquisitionList{}, fmt.Errorf("%w: invalid cursor", ErrInvalid)
		}
	}
	tx, err := audiobookAdminTx(ctx, s, v)
	if err != nil {
		return AudiobookAcquisitionList{}, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT `+audiobookAcquisitionColumns+`,r.updated_at`+audiobookAcquisitionFrom+`
 WHERE r.organization_id=$1 AND r.media_type='audiobook' AND (`+filter+`)
 AND ($2::text='' OR (r.updated_at,r.id)<($3::timestamptz,$2::uuid))
 ORDER BY r.updated_at DESC,r.id DESC LIMIT $4`, v.OrganizationID, after.ID, after.UpdatedAt, limit+1)
	if err != nil {
		return AudiobookAcquisitionList{}, err
	}
	type row struct {
		item    AudiobookAcquisition
		id      string
		updated time.Time
	}
	found := []row{}
	for rows.Next() {
		var out AudiobookAcquisition
		var id, submission string
		var descriptor []byte
		var updated time.Time
		if err = rows.Scan(&id, &out.Dispatched, &out.Attempt, &out.AttemptStartedAt, &out.ProviderStatus, &out.ExternalStatus, &out.ReviewKind,
			&out.SearchPasses, &out.FailureCount, &out.AICalls, &out.NextAttemptAt, &out.ProviderCompletedAt, &out.ApprovedCandidate,
			&submission, &descriptor, &out.StuckReason, &updated); err != nil {
			rows.Close()
			return AudiobookAcquisitionList{}, err
		}
		out.Stuck = out.StuckReason != ""
		if out.Dispatched && submission != "" && out.Attempt > 0 {
			out.Marker = AudiobookMarker(submission, out.Attempt)
		}
		found = append(found, row{item: out, id: id, updated: updated})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return AudiobookAcquisitionList{}, err
	}
	list := AudiobookAcquisitionList{Acquisitions: []AudiobookAcquisition{}}
	if len(found) > limit {
		last := found[limit-1]
		raw, _ := json.Marshal(audiobookCursor{UpdatedAt: last.updated, ID: last.id})
		list.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		found = found[:limit]
	}
	for _, f := range found {
		if f.item.Request, err = scan(tx.QueryRow(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE id=$1`, f.id)); err != nil {
			return AudiobookAcquisitionList{}, err
		}
		list.Acquisitions = append(list.Acquisitions, f.item)
	}
	return list, nil
}

// lockAudiobookAction locks an approved, active audiobook request at the
// expected revision for an admin acquisition action.
func lockAudiobookAction(ctx context.Context, tx pgx.Tx, v Viewer, id string, expected int64) (Request, error) {
	if expected <= 0 {
		return Request{}, ErrInvalid
	}
	if _, err := uuid.Parse(id); err != nil {
		return Request{}, ErrNotFound
	}
	r, err := scan(tx.QueryRow(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE id=$1 AND organization_id=$2 AND media_type='audiobook' FOR UPDATE`, id, v.OrganizationID))
	if err != nil {
		return r, err
	}
	if !canRead(v, r) {
		return Request{}, ErrNotFound
	}
	if r.Revision != expected || r.Outcome != "active" || r.Status != "approved" {
		return Request{}, ErrConflict
	}
	return r, nil
}

type audiobookDispatchState struct {
	descriptor     []byte
	providerStatus string
	externalStatus string
	reviewKind     string
	attempt        int
	startedAt      time.Time
	failures       int
	stuck          bool
}

func lockAudiobookDispatch(ctx context.Context, tx pgx.Tx, id string) (audiobookDispatchState, error) {
	var st audiobookDispatchState
	err := tx.QueryRow(ctx, `SELECT d.descriptor,d.provider_status,a.external_status,COALESCE(a.review_kind,''),a.attempt,a.attempt_started_at,d.failure_count,
 (d.provider_status IN ('queued','downloading') AND (a.attempt_started_at<now()-interval '72 hours' OR a.external_status='stalled'))
 FROM bloem_ebook_request_dispatches d JOIN bloem_audiobook_request_acquisitions a ON a.request_id=d.request_id WHERE d.request_id=$1 FOR UPDATE OF d,a`, id).Scan(
		&st.descriptor, &st.providerStatus, &st.externalStatus, &st.reviewKind, &st.attempt, &st.startedAt, &st.failures, &st.stuck)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, ErrConflict
	}
	return st, err
}

// requireLiveAudiobookIdentity refuses an action whose frozen backend was
// removed or replaced; the admin cancels and refiles, or fulfills manually.
func requireLiveAudiobookIdentity(ctx context.Context, tx pgx.Tx, org uuid.UUID, descriptor []byte) error {
	var frozen AudiobookIntegration
	if err := json.Unmarshal(descriptor, &frozen); err != nil {
		return err
	}
	current, err := loadAudiobookIdentity(ctx, tx, org, frozen.ID)
	if errors.Is(err, ErrConflict) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if !sameAudiobookIdentity(current, frozen) {
		return ErrConflict
	}
	return nil
}

// startAudiobookAttemptTx starts a new attempt: new tag and marker, counters
// reset. The lease is never touched: it is the only record that a provider
// call for this submission may still be in flight.
func startAudiobookAttemptTx(ctx context.Context, tx pgx.Tx, id string, approved *string) error {
	if _, err := tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET started=false,external_id='',provider_status='unknown',failure_count=0,next_attempt_at=now(),updated_at=now() WHERE request_id=$1`, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE bloem_audiobook_request_acquisitions SET attempt=attempt+1,attempt_started_at=now(),search_passes=0,absent_checks=0,ai_calls=0,
 external_status='',review_kind=NULL,approved_candidate=$2,provider_completed_at=NULL,updated_at=now() WHERE request_id=$1 AND attempt<1000`, id, approved)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// retryAudiobookTx starts a new attempt after a failure, a review or a stuck
// download, or rechecks an uncertain one. It returns the new fulfilment and
// the requester-safe reason.
func (s *Service) retryAudiobookTx(ctx context.Context, tx pgx.Tx, r Request) (string, string, error) {
	st, err := lockAudiobookDispatch(ctx, tx, r.ID)
	if err != nil {
		return "", "", err
	}
	if err = requireLiveAudiobookIdentity(ctx, tx, r.OrganizationID, st.descriptor); err != nil {
		return "", "", err
	}
	switch {
	case st.providerStatus == "failed" || st.providerStatus == "review" || r.Fulfilment == "failed" || r.Fulfilment == "review" || st.stuck:
		if err = startAudiobookAttemptTx(ctx, tx, r.ID, nil); err != nil {
			return "", "", err
		}
		return "queued", "Starting a new search.", nil
	case r.Fulfilment == "retrying":
		_, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET failure_count=0,next_attempt_at=now(),updated_at=now() WHERE request_id=$1`, r.ID)
		return "retrying", "Checking the existing download again.", err
	default:
		return "", "", ErrConflict
	}
}

// ApproveAudiobookCandidate starts a new attempt that downloads a release an
// admin chose from the newest held decision of the current attempt.
func (s *Service) ApproveAudiobookCandidate(ctx context.Context, v Viewer, id string, in AudiobookCandidateApproval) (Request, error) {
	if !IsAudiobookCandidateRef(in.CandidateRef) {
		return Request{}, fmt.Errorf("%w: unknown candidate", ErrInvalid)
	}
	tx, err := audiobookAdminTx(ctx, s, v)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback(ctx)
	r, err := lockAudiobookAction(ctx, tx, v, id, in.ExpectedRevision)
	if err != nil {
		return Request{}, err
	}
	st, err := lockAudiobookDispatch(ctx, tx, id)
	if err != nil {
		return Request{}, err
	}
	if st.providerStatus != "review" || st.reviewKind != "selection" {
		return Request{}, ErrConflict
	}
	if err = requireLiveAudiobookIdentity(ctx, tx, r.OrganizationID, st.descriptor); err != nil {
		return Request{}, err
	}
	var envelope []byte
	err = tx.QueryRow(ctx, `SELECT envelope FROM bloem_audiobook_request_decisions WHERE request_id=$1 AND attempt=$2 AND phase='select' AND outcome='held' ORDER BY id DESC LIMIT 1`, id, st.attempt).Scan(&envelope)
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrConflict
	}
	if err != nil {
		return Request{}, err
	}
	var held AcquisitionDecision
	if json.Unmarshal(envelope, &held) != nil || held.Validate() != nil {
		return Request{}, ErrConflict
	}
	index := slices.IndexFunc(held.Candidates, func(c DecisionCandidate) bool { return c.Ref == in.CandidateRef })
	if index < 0 {
		return Request{}, ErrConflict
	}
	chosen := held.Candidates[index]
	if err = startAudiobookAttemptTx(ctx, tx, id, &in.CandidateRef); err != nil {
		return Request{}, err
	}
	approved := &AcquisitionDecision{V: 1, Phase: "admin", Outcome: "admin_approved", Chosen: &chosen, AdminApproved: true}
	raw, err := approved.Encode()
	if err != nil {
		return Request{}, err
	}
	if err = insertAudiobookDecisionTx(ctx, tx, id, st.attempt+1, "admin", "admin_approved", raw, false); err != nil {
		return Request{}, err
	}
	if r, err = updateEbookState(ctx, tx, r, "approved", "queued", "", ""); err != nil {
		return Request{}, err
	}
	return r, tx.Commit(ctx)
}

// AcceptAudiobookDownload accepts a download held by verification. Path
// completion then runs on the next pass. An ambiguous multiple-item folder
// is refused: the admin fulfills that request manually.
func (s *Service) AcceptAudiobookDownload(ctx context.Context, v Viewer, id string, in ActionInput) (Request, error) {
	tx, err := audiobookAdminTx(ctx, s, v)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback(ctx)
	r, err := lockAudiobookAction(ctx, tx, v, id, in.ExpectedRevision)
	if err != nil {
		return Request{}, err
	}
	st, err := lockAudiobookDispatch(ctx, tx, id)
	if err != nil {
		return Request{}, err
	}
	if st.providerStatus != "review" || st.reviewKind != "verification" {
		return Request{}, ErrConflict
	}
	var envelope []byte
	err = tx.QueryRow(ctx, `SELECT envelope FROM bloem_audiobook_request_decisions WHERE request_id=$1 AND attempt=$2 AND phase='verify' ORDER BY id DESC LIMIT 1`, id, st.attempt).Scan(&envelope)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Request{}, err
	}
	if err == nil {
		var verify AcquisitionDecision
		if json.Unmarshal(envelope, &verify) == nil && slices.Contains(verify.Issues, "multiple_items") {
			return Request{}, ErrConflict
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET provider_status='completed',next_attempt_at=now(),updated_at=now() WHERE request_id=$1`, id); err != nil {
		return Request{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_audiobook_request_acquisitions SET review_kind=NULL,provider_completed_at=now(),updated_at=now() WHERE request_id=$1`, id); err != nil {
		return Request{}, err
	}
	accepted := &AcquisitionDecision{V: 1, Phase: "admin", Outcome: "admin_accepted", AdminApproved: true}
	raw, err := accepted.Encode()
	if err != nil {
		return Request{}, err
	}
	if err = insertAudiobookDecisionTx(ctx, tx, id, st.attempt, "admin", "admin_accepted", raw, false); err != nil {
		return Request{}, err
	}
	if r, err = updateEbookState(ctx, tx, r, "approved", "awaiting_library", "", ""); err != nil {
		return Request{}, err
	}
	return r, tx.Commit(ctx)
}
