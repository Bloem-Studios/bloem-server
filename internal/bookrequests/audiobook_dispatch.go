package bookrequests

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	audiobookMaxFailures       = 5
	audiobookNoMatchDelay      = 6 * time.Hour
	audiobookUnavailableDelay  = 30 * time.Minute
	audiobookPollDelay         = 60 * time.Second
	audiobookSearchWindow      = 7 * 24 * time.Hour
	audiobookMaxSearchesPerRun = 5
	audiobookRunBudget         = 3 * time.Minute
	audiobookMaxDecisions      = 50
	audiobookAbsentResets      = 2
	audiobookRecovered         = "recovered"
)

// Requester-safe reasons. Release names, refs, client names and paths stay
// in the admin view.
const (
	audiobookReasonReview        = "Waiting for an administrator to review the download."
	audiobookReasonWindow        = "No suitable release was found within seven days."
	audiobookReasonRejected      = "The release could not be handed to the download client."
	audiobookReasonConfig        = "The acquisition backend rejected its configuration."
	audiobookReasonFailed        = "The download failed."
	audiobookReasonLost          = "The download could not be found; retry starts a new search."
	audiobookReasonBackendChange = "The acquisition backend was removed or replaced."
)

// errAudiobookNotCalled marks a work check that failed before any provider
// call, so a Fulfill that never ran is not treated as uncertain.
var errAudiobookNotCalled = ErrProviderNotCalled

type audiobookWork struct {
	input          AudiobookDispatchInput
	token          uuid.UUID
	until          time.Time
	call           string // fulfill | recover | check
	receipt        Receipt
	providerStatus string
	failures       int
	absent         int
	policyEpoch    int64
	startedAt      time.Time
}

type audiobookNotice struct {
	request Request
	attempt int
}

type audiobookAttentionNotifier interface {
	Attention(context.Context, Request, int)
}

func (s *Service) audiobookClock() time.Time {
	if s.audiobookNow != nil {
		return s.audiobookNow()
	}
	return time.Now()
}

func (s *Service) sendAudiobookAttention(ctx context.Context, notices []audiobookNotice) {
	n, ok := s.notifier.(audiobookAttentionNotifier)
	if !ok {
		return
	}
	for _, notice := range notices {
		n.Attention(ctx, notice.request, notice.attempt)
	}
}

// ReconcileAudiobooks dispatches approved audiobook requests to the composed
// acquisition backend. Work is leased in PostgreSQL and provider calls run
// outside transactions. The run budget is checked before each claim, so no
// row is marked started without its call.
func (s *Service) ReconcileAudiobooks(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id FROM bloem_book_requests r LEFT JOIN bloem_ebook_request_dispatches d ON d.request_id=r.id
 WHERE r.media_type='audiobook' AND r.outcome='active' AND r.status='approved'
 AND (d.request_id IS NULL OR (d.provider_status NOT IN ('review','failed') AND d.failure_count<$2 AND d.next_attempt_at<=now() AND (d.lease_until IS NULL OR d.lease_until<=now())))
 ORDER BY r.last_checked_at NULLS FIRST,r.created_at,r.id LIMIT $1`, limit, audiobookMaxFailures)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	deadline := s.audiobookClock().Add(audiobookRunBudget)
	searches, completed := 0, 0
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return completed, err
		}
		if !s.audiobookClock().Before(deadline) {
			break
		}
		work, done, notices, err := s.claimAudiobook(ctx, id, searches < audiobookMaxSearchesPerRun)
		s.sendAudiobookAttention(ctx, notices)
		if err != nil {
			return completed, err
		}
		if done {
			completed++
			continue
		}
		if work == nil {
			continue
		}
		if work.call == "fulfill" {
			searches++
		}
		outcome, callErr := s.callAudiobook(ctx, work)
		if callErr == nil {
			if err = s.saveAudiobookOutcome(ctx, work, outcome); err != nil {
				return completed, err
			}
		}
		done, notices, err = s.finishAudiobook(ctx, work, outcome, callErr)
		s.sendAudiobookAttention(ctx, notices)
		if err != nil {
			return completed, err
		}
		if done {
			completed++
		}
	}
	return completed, nil
}

func (s *Service) callAudiobook(ctx context.Context, work *audiobookWork) (AudiobookOutcome, error) {
	callCtx, cancel := context.WithDeadline(ctx, work.until.Add(-5*time.Second))
	defer cancel()
	if err := s.validateAudiobookWork(callCtx, work); err != nil {
		return AudiobookOutcome{}, errors.Join(errAudiobookNotCalled, err)
	}
	switch work.call {
	case "fulfill":
		return s.audiobookFulfillment.Fulfill(callCtx, work.input)
	case "recover":
		receipt, err := s.audiobookFulfillment.Recover(callCtx, work.input)
		return AudiobookOutcome{Kind: audiobookRecovered, Receipt: receipt}, err
	default:
		return s.audiobookFulfillment.CheckStatus(callCtx, work.input, work.receipt)
	}
}

// saveAudiobookOutcome persists the receipt and decision before the outcome
// fence, so a hand-off that outlives this lease is never forgotten. A late
// receipt from an earlier attempt is dropped, never applied to a newer one.
func (s *Service) saveAudiobookOutcome(ctx context.Context, work *audiobookWork, outcome AudiobookOutcome) error {
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if outcome.Receipt.ExternalID != "" && (outcome.Kind == AudiobookHandedOff || outcome.Kind == audiobookRecovered) {
		if err := s.saveAudiobookReceipt(saveCtx, work, outcome.Receipt); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	if outcome.Decision != nil {
		return s.saveAudiobookDecision(saveCtx, work.input.Request.ID, work.input.Attempt, outcome.Decision)
	}
	return nil
}

func (s *Service) saveAudiobookReceipt(ctx context.Context, work *audiobookWork, receipt Receipt) error {
	if _, err := ParseAudiobookReceipt(receipt.ExternalID, work.input.SubmissionID, work.input.Attempt); err != nil {
		return ErrInvalid
	}
	descriptor, err := json.Marshal(work.input.Integration)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE bloem_ebook_request_dispatches d SET external_id=$2,updated_at=now() FROM bloem_audiobook_request_acquisitions a
 WHERE d.request_id=$1 AND a.request_id=d.request_id AND a.attempt=$4 AND d.descriptor=$3::jsonb AND (d.external_id='' OR d.external_id=$2)`, work.input.Request.ID, receipt.ExternalID, descriptor, work.input.Attempt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// saveAudiobookDecision appends an attempt-scoped decision, adds its AI calls
// to the attempt's count and trims the request to its newest decisions.
func (s *Service) saveAudiobookDecision(ctx context.Context, requestID string, attempt int, decision *AcquisitionDecision) error {
	envelope, err := decision.Encode()
	if err != nil {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = insertAudiobookDecisionTx(ctx, tx, requestID, attempt, decision.Phase, decision.Outcome, envelope, true); err != nil {
		return err
	}
	calls := min(max(decision.AICalls, 0), 10)
	if _, err = tx.Exec(ctx, `UPDATE bloem_audiobook_request_acquisitions SET ai_calls=ai_calls+$3,updated_at=now() WHERE request_id=$1 AND attempt=$2`, requestID, attempt, calls); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertAudiobookDecisionTx(ctx context.Context, tx pgx.Tx, requestID string, attempt int, phase, outcome string, envelope []byte, requireAttempt bool) error {
	_, err := tx.Exec(ctx, `INSERT INTO bloem_audiobook_request_decisions(request_id,attempt,phase,outcome,envelope)
 SELECT $1,$2,$3,$4,$5::jsonb WHERE NOT $6 OR EXISTS(SELECT 1 FROM bloem_audiobook_request_acquisitions WHERE request_id=$1 AND attempt=$2)`, requestID, attempt, phase, outcome, envelope, requireAttempt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM bloem_audiobook_request_decisions WHERE request_id=$1 AND id NOT IN (SELECT id FROM bloem_audiobook_request_decisions WHERE request_id=$1 ORDER BY id DESC LIMIT $2)`, requestID, audiobookMaxDecisions)
	return err
}

// audiobookFenceKeys covers every identifier of the request, so a refile
// with a different identifier set still serializes with the earlier chain.
func audiobookFenceKeys(r Request, identity, integration string) []string {
	keys := []string{identity}
	if r.ISBN != "" {
		keys = append(keys, "isbn:"+r.ISBN)
	}
	if r.ASIN != "" {
		keys = append(keys, "asin:"+r.ASIN)
	}
	sort.Strings(keys)
	keys = slices.Compact(keys)
	for i := range keys {
		keys[i] = "bloem-audiobook:" + r.OrganizationID.String() + ":" + integration + ":" + keys[i]
	}
	return keys
}

type audiobookPrior struct {
	descriptor     []byte
	submission     string
	started        bool
	externalID     string
	providerStatus string
	attempt        int
	startedAt      time.Time
	searchPasses   int
	aiCalls        int
	externalStatus string
	completedAt    *time.Time
	approved       *string
}

func (s *Service) claimAudiobook(ctx context.Context, id string, allowFulfill bool) (*audiobookWork, bool, []audiobookNotice, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, nil, err
	}
	defer tx.Rollback(ctx)
	r, err := scan(tx.QueryRow(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE id=$1 AND media_type='audiobook' AND outcome='active' AND status='approved' FOR UPDATE SKIP LOCKED`, id))
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil, nil
	}
	if err != nil {
		return nil, false, nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_book_requests SET last_checked_at=now() WHERE id=$1`, id); err != nil {
		return nil, false, nil, err
	}
	requester := Viewer{UserID: r.RequestedByUserID, ProfileID: r.RequestedByProfileID, OrganizationID: r.OrganizationID}
	if err = checkAudiobookAuthority(ctx, tx, requester); err == nil {
		err = audiobookRequestsEnabled(ctx, tx)
	}
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrDisabled) {
		return nil, false, nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, false, nil, err
	}
	// Library match A: an exact identifier match is real evidence.
	match, err := findLibraryMatch(ctx, tx, r)
	if err != nil {
		return nil, false, nil, err
	}
	if match != "" {
		if _, err = updateEbookState(ctx, tx, r, "completed", "library", "", match); err != nil {
			return nil, false, nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET lease_token=NULL,lease_until=NULL WHERE request_id=$1`, id); err != nil {
			return nil, false, nil, err
		}
		return nil, true, nil, tx.Commit(ctx)
	}
	work := &audiobookWork{input: AudiobookDispatchInput{Request: r}, token: uuid.New()}
	var descriptor []byte
	var started bool
	var leaseUntil, completedAt *time.Time
	var next time.Time
	var approved *string
	var attentionAttempt int
	err = tx.QueryRow(ctx, `SELECT d.descriptor,d.started,d.external_id,d.provider_status,d.failure_count,d.lease_until,d.next_attempt_at,d.submission_request_id,
 a.attempt,a.attempt_started_at,a.search_passes,a.ai_calls,a.absent_checks,a.approved_candidate,a.provider_completed_at,a.attention_notified_attempt
 FROM bloem_ebook_request_dispatches d JOIN bloem_audiobook_request_acquisitions a ON a.request_id=d.request_id WHERE d.request_id=$1 FOR UPDATE OF d,a`, id).Scan(
		&descriptor, &started, &work.receipt.ExternalID, &work.providerStatus, &work.failures, &leaseUntil, &next, &work.input.SubmissionID,
		&work.input.Attempt, &work.startedAt, &work.input.SearchPass, &work.input.AICalls, &work.absent, &approved, &completedAt, &attentionAttempt)
	switch {
	case err == nil:
		now := time.Now()
		if work.failures >= audiobookMaxFailures || next.After(now) || leaseUntil != nil && leaseUntil.After(now) || work.providerStatus == "review" || work.providerStatus == "failed" {
			return nil, false, nil, tx.Commit(ctx)
		}
		if err = json.Unmarshal(descriptor, &work.input.Integration); err != nil {
			return nil, false, nil, err
		}
		if work.providerStatus != "completed" {
			current, checkErr := loadAudiobookIdentity(ctx, tx, r.OrganizationID, work.input.Integration.ID)
			if checkErr != nil && !errors.Is(checkErr, ErrConflict) {
				return nil, false, nil, checkErr
			}
			if checkErr != nil || !sameAudiobookIdentity(current, work.input.Integration) {
				r, err = updateEbookState(ctx, tx, r, "approved", "failed", audiobookReasonBackendChange, "")
				if err != nil {
					return nil, false, nil, err
				}
				if _, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET provider_status='failed',failure_count=$2,updated_at=now() WHERE request_id=$1`, id, audiobookMaxFailures); err != nil {
					return nil, false, nil, err
				}
				notices, err := markAudiobookAttentionTx(ctx, tx, r, work.input.Attempt, attentionAttempt)
				if err != nil {
					return nil, false, nil, err
				}
				return nil, false, notices, tx.Commit(ctx)
			}
		}
	case errors.Is(err, pgx.ErrNoRows):
		current, err := selectAudiobookIntegration(ctx, tx, r.OrganizationID)
		if errors.Is(err, ErrConflict) {
			return nil, false, nil, tx.Commit(ctx)
		}
		if err != nil {
			return nil, false, nil, err
		}
		var identity string
		if err = tx.QueryRow(ctx, `SELECT identity_key FROM bloem_book_requests WHERE id=$1`, id).Scan(&identity); err != nil {
			return nil, false, nil, err
		}
		for _, key := range audiobookFenceKeys(r, identity, current.ID) {
			if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
				return nil, false, nil, err
			}
		}
		var prior audiobookPrior
		err = tx.QueryRow(ctx, `SELECT d.descriptor,d.submission_request_id,d.started,d.external_id,d.provider_status,a.attempt,a.attempt_started_at,a.search_passes,a.ai_calls,a.external_status,a.provider_completed_at,a.approved_candidate
 FROM bloem_ebook_request_dispatches d JOIN bloem_audiobook_request_acquisitions a ON a.request_id=d.request_id JOIN bloem_book_requests prior ON prior.id=d.request_id
 WHERE prior.organization_id=$1 AND prior.media_type='audiobook' AND d.integration_id=$2 AND prior.id<>$3
 AND (prior.identity_key=$4 OR ($5<>'' AND prior.isbn=$5) OR ($6<>'' AND prior.asin=$6))
 ORDER BY a.attempt DESC,d.updated_at DESC,d.request_id LIMIT 1 FOR SHARE OF d,a`, r.OrganizationID, current.ID, id, identity, r.ISBN, r.ASIN).Scan(
			&prior.descriptor, &prior.submission, &prior.started, &prior.externalID, &prior.providerStatus, &prior.attempt, &prior.startedAt, &prior.searchPasses, &prior.aiCalls, &prior.externalStatus, &prior.completedAt, &prior.approved)
		adopt := false
		switch {
		case err == nil && prior.started && prior.providerStatus != "failed" && prior.providerStatus != "review":
			// D4: the newest attempt of the chain is still live; adopt it.
			adopt = true
			work.input.SubmissionID, work.input.Attempt = prior.submission, prior.attempt
			if err = json.Unmarshal(prior.descriptor, &work.input.Integration); err != nil {
				return nil, false, nil, err
			}
			descriptor = prior.descriptor
		case err == nil:
			work.input.SubmissionID, work.input.Attempt = prior.submission, min(prior.attempt+1, 1000)
		case errors.Is(err, pgx.ErrNoRows):
			work.input.SubmissionID, work.input.Attempt = id, 1
		default:
			return nil, false, nil, err
		}
		if !adopt {
			work.input.Integration = current
			if descriptor, err = json.Marshal(current); err != nil {
				return nil, false, nil, err
			}
		}
		// Preflight is check-then-act: never start a second provider call for
		// a submission while any of its rows holds a live lease.
		var inFlight bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_ebook_request_dispatches WHERE submission_request_id=$1 AND lease_until>now())`, work.input.SubmissionID).Scan(&inFlight); err != nil {
			return nil, false, nil, err
		}
		if inFlight {
			return nil, false, nil, tx.Commit(ctx)
		}
		work.providerStatus = "unknown"
		if adopt {
			started, work.receipt.ExternalID, work.providerStatus = true, prior.externalID, prior.providerStatus
			work.startedAt, work.input.SearchPass, work.input.AICalls, completedAt, approved = prior.startedAt, prior.searchPasses, prior.aiCalls, prior.completedAt, prior.approved
		}
		if _, err = tx.Exec(ctx, `INSERT INTO bloem_ebook_request_dispatches(request_id,integration_id,submission_request_id,descriptor,started,external_id,provider_status) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, current.ID, work.input.SubmissionID, descriptor, started, work.receipt.ExternalID, work.providerStatus); err != nil {
			return nil, false, nil, err
		}
		if !adopt {
			work.startedAt = time.Now()
			prior.externalStatus = ""
		}
		if _, err = tx.Exec(ctx, `INSERT INTO bloem_audiobook_request_acquisitions(request_id,attempt,attempt_started_at,search_passes,ai_calls,external_status,provider_completed_at,approved_candidate) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, work.input.Attempt, work.startedAt, work.input.SearchPass, work.input.AICalls, prior.externalStatus, completedAt, approved); err != nil {
			return nil, false, nil, err
		}
		if !sameAudiobookIdentity(current, work.input.Integration) {
			// The adopted chain belongs to a replaced backend; its outcome
			// cannot be resumed safely.
			if r, err = updateEbookState(ctx, tx, r, "approved", "failed", audiobookReasonBackendChange, ""); err != nil {
				return nil, false, nil, err
			}
			if _, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET provider_status='failed',failure_count=$2 WHERE request_id=$1`, id, audiobookMaxFailures); err != nil {
				return nil, false, nil, err
			}
			notices, err := markAudiobookAttentionTx(ctx, tx, r, work.input.Attempt, 0)
			if err != nil {
				return nil, false, nil, err
			}
			return nil, false, notices, tx.Commit(ctx)
		}
	default:
		return nil, false, nil, err
	}
	if approved != nil {
		work.input.ApprovedCandidate = *approved
	}
	if work.providerStatus == "completed" {
		done, notices, err := s.completeAudiobookByPathTx(ctx, tx, r, work, completedAt, attentionAttempt)
		if err != nil {
			return nil, false, nil, err
		}
		return nil, done, notices, tx.Commit(ctx)
	}
	if err = tx.QueryRow(ctx, `SELECT revision FROM bloem_ebook_request_policy_epoch WHERE id FOR SHARE`).Scan(&work.policyEpoch); err != nil {
		return nil, false, nil, err
	}
	switch {
	case work.receipt.ExternalID != "":
		work.call = "check"
	case started:
		work.call = "recover"
	default:
		work.call = "fulfill"
	}
	// Do not mark a submission started if no composed host can execute it,
	// or if this run's search budget is spent.
	if s.audiobookFulfillment == nil || work.call == "fulfill" && !allowFulfill {
		return nil, false, nil, tx.Commit(ctx)
	}
	// SearchPass holds the stored count of finished passes until here.
	searchPasses := work.input.SearchPass
	work.input.SearchPass = max(searchPasses, 1)
	if work.call == "fulfill" {
		work.input.SearchPass = searchPasses + 1
		if work.input.ExcludedRefs, err = audiobookExcludedRefs(ctx, tx, work.input.SubmissionID, work.input.Attempt, work.input.ApprovedCandidate); err != nil {
			return nil, false, nil, err
		}
	}
	err = tx.QueryRow(ctx, `UPDATE bloem_ebook_request_dispatches SET started=started OR $3,lease_token=$2,lease_until=now()+make_interval(secs=>$4),updated_at=now() WHERE request_id=$1 RETURNING lease_until`, id, work.token, work.call == "fulfill", ebookLeaseSeconds).Scan(&work.until)
	if err != nil {
		return nil, false, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, nil, err
	}
	return work, false, nil, nil
}

// audiobookExcludedRefs returns releases chosen by earlier attempts of the
// submission chain. An attempt only ends through failure, review or a stuck
// retry, so every earlier hand-off is excluded from new searches.
func audiobookExcludedRefs(ctx context.Context, tx pgx.Tx, submission string, attempt int, approved string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT dec.envelope->'chosen'->>'ref',max(dec.id) FROM bloem_audiobook_request_decisions dec
 JOIN bloem_ebook_request_dispatches d ON d.request_id=dec.request_id
 WHERE d.submission_request_id=$1 AND dec.attempt<$2 AND dec.outcome IN ('handed_off','adopted') AND dec.envelope->'chosen'->>'ref' IS NOT NULL
 GROUP BY 1 ORDER BY 2 DESC LIMIT 10`, submission, attempt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var ref string
		var newest int64
		if err = rows.Scan(&ref, &newest); err != nil {
			return nil, err
		}
		if ref != approved && IsAudiobookCandidateRef(ref) {
			out = append(out, ref)
		}
	}
	return out, rows.Err()
}

func (s *Service) validateAudiobookWork(ctx context.Context, work *audiobookWork) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	return checkAudiobookWorkTx(ctx, tx, work)
}

// checkAudiobookWorkTx is the full fence: live lease, the request still
// active and approved, the requester's authority, global requests, the
// frozen identity, the policy epoch and the claimed attempt.
func checkAudiobookWorkTx(ctx context.Context, tx pgx.Tx, work *audiobookWork) error {
	r := work.input.Request
	var live bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_book_requests r JOIN bloem_ebook_request_dispatches d ON d.request_id=r.id JOIN bloem_audiobook_request_acquisitions a ON a.request_id=d.request_id
 WHERE r.id=$1 AND r.outcome='active' AND r.status='approved' AND d.lease_token=$2 AND d.lease_until>now() AND a.attempt=$3)`, r.ID, work.token, work.input.Attempt).Scan(&live)
	if err != nil {
		return err
	}
	if !live {
		return ErrConflict
	}
	if err = checkAudiobookAuthority(ctx, tx, Viewer{UserID: r.RequestedByUserID, ProfileID: r.RequestedByProfileID, OrganizationID: r.OrganizationID}); err != nil {
		return err
	}
	if err = audiobookRequestsEnabled(ctx, tx); err != nil {
		return err
	}
	current, err := loadAudiobookIdentity(ctx, tx, r.OrganizationID, work.input.Integration.ID)
	if err != nil {
		return err
	}
	if !sameAudiobookIdentity(current, work.input.Integration) {
		return ErrConflict
	}
	var epoch int64
	if err = tx.QueryRow(ctx, `SELECT revision FROM bloem_ebook_request_policy_epoch WHERE id FOR SHARE`).Scan(&epoch); err != nil {
		return err
	}
	if epoch != work.policyEpoch {
		return ErrConflict
	}
	return nil
}

// ValidateAudiobookIntegration re-checks a frozen identity for the adapter.
func (s *Service) ValidateAudiobookIntegration(ctx context.Context, in AudiobookIntegration) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = audiobookRequestsEnabled(ctx, tx); err != nil {
		return err
	}
	current, err := loadAudiobookIdentity(ctx, tx, in.OrganizationID, in.ID)
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	if !sameAudiobookIdentity(current, in) {
		return ErrConflict
	}
	return nil
}

type audiobookApply struct {
	fulfilment     string
	reason         string
	providerStatus string
	failures       int
	delay          time.Duration
	started        *bool
	clearReceipt   bool
	externalStatus *string
	absent         int
	searchPass     bool
	reviewKind     *string
	providerDone   bool
	unreadable     bool
}

func (s *Service) finishAudiobook(ctx context.Context, work *audiobookWork, outcome AudiobookOutcome, callErr error) (bool, []audiobookNotice, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback(ctx)
	r, err := scan(tx.QueryRow(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE id=$1 FOR UPDATE`, work.input.Request.ID))
	if err != nil {
		return false, nil, err
	}
	var token *uuid.UUID
	var until, completedAt *time.Time
	var attentionAttempt int
	if err = tx.QueryRow(ctx, `SELECT d.lease_token,d.lease_until,a.provider_completed_at,a.attention_notified_attempt FROM bloem_ebook_request_dispatches d JOIN bloem_audiobook_request_acquisitions a ON a.request_id=d.request_id WHERE d.request_id=$1 FOR UPDATE OF d,a`, r.ID).Scan(&token, &until, &completedAt, &attentionAttempt); err != nil {
		return false, nil, err
	}
	if token == nil || *token != work.token || until == nil || !until.After(time.Now()) {
		return false, nil, nil
	}
	notCalled := errors.Is(callErr, errAudiobookNotCalled)
	if err = checkAudiobookWorkTx(ctx, tx, work); err != nil || notCalled {
		if err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrDisabled) && !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrInvalid) {
			return false, nil, err
		}
		// Nothing applies. A Fulfill that never ran is not left started.
		_, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET lease_token=NULL,lease_until=NULL,started=started AND NOT $3,next_attempt_at=now()+interval '30 seconds' WHERE request_id=$1 AND lease_token=$2`, r.ID, work.token, notCalled && work.call == "fulfill")
		if err != nil {
			return false, nil, err
		}
		return false, nil, tx.Commit(ctx)
	}
	apply := audiobookApply{fulfilment: r.Fulfilment, providerStatus: work.providerStatus, failures: work.failures, delay: audiobookPollDelay, absent: work.absent}
	kind := outcome.Kind
	if callErr != nil {
		kind = AudiobookUncertain
	}
	setStarted := func(v bool) { apply.started = &v }
	setExternal := func(v string) { apply.externalStatus = &v }
	setReview := func(v string) { apply.reviewKind = &v }
	searchingOrExpired := func(delay time.Duration) {
		setStarted(false)
		apply.clearReceipt, apply.searchPass, apply.delay = true, true, delay
		apply.providerStatus, apply.fulfilment, apply.reason, apply.failures, apply.absent = "searching", "searching", "", 0, 0
		setExternal("")
		if time.Now().After(work.startedAt.Add(audiobookSearchWindow)) {
			apply.providerStatus, apply.fulfilment, apply.reason, apply.failures = "failed", "failed", audiobookReasonWindow, audiobookMaxFailures
		}
	}
	failed := func(reason string) {
		apply.providerStatus, apply.fulfilment, apply.reason, apply.failures = "failed", "failed", reason, audiobookMaxFailures
	}
	unknown := func() {
		apply.failures = work.failures + 1
		apply.delay = time.Duration(min(900, 30*(1<<min(apply.failures-1, 5)))) * time.Second
		apply.providerStatus, apply.fulfilment, apply.reason = "unknown", "retrying", ""
		if apply.failures >= audiobookMaxFailures {
			failed(audiobookReasonLost)
		}
	}
	switch kind {
	case AudiobookHandedOff:
		status := outcome.ProviderStatus
		if status != "downloading" {
			status = "queued"
		}
		apply.providerStatus, apply.failures, apply.absent = status, 0, 0
		apply.fulfilment, apply.reason = status, ""
		setExternal(outcome.ExternalStatus)
	case audiobookRecovered:
		apply.providerStatus, apply.fulfilment, apply.reason = "unknown", "retrying", ""
	case AudiobookNoMatch:
		searchingOrExpired(audiobookNoMatchDelay)
	case AudiobookUnavailable:
		searchingOrExpired(audiobookUnavailableDelay)
	case AudiobookHeld:
		setStarted(false)
		apply.clearReceipt = true
		apply.providerStatus, apply.fulfilment, apply.reason, apply.failures = "review", "review", audiobookReasonReview, 0
		setReview("selection")
	case AudiobookRejected:
		setStarted(false)
		failed(audiobookReasonRejected)
	case AudiobookConfigRejected:
		setStarted(false)
		failed(audiobookReasonConfig)
	case AudiobookStatus:
		setExternal(outcome.ExternalStatus)
		switch outcome.ProviderStatus {
		case "queued", "downloading":
			apply.providerStatus, apply.fulfilment, apply.reason, apply.failures, apply.absent = outcome.ProviderStatus, outcome.ProviderStatus, "", 0, 0
		case "completed":
			apply.providerStatus, apply.fulfilment, apply.reason, apply.failures, apply.absent = "completed", "awaiting_library", "", 0, 0
			apply.providerDone = true
		case "review":
			apply.providerStatus, apply.fulfilment, apply.reason, apply.failures = "review", "review", audiobookReasonReview, 0
			setReview("verification")
		case "failed":
			failed(audiobookReasonFailed)
		case AudiobookProviderUnreadable:
			// The download client could not be read. Keep the state and
			// counters: only an answer about the download itself moves the
			// request, so an outage never fails it as lost.
			apply.reason, apply.unreadable = r.OutcomeReason, true
		default:
			if outcome.ExternalStatus == "absent" {
				apply.absent = work.absent + 1
				apply.providerStatus, apply.fulfilment, apply.reason = "unknown", "retrying", ""
				if apply.absent >= audiobookAbsentResets {
					// The hand-off never appeared: search again; preflight
					// adopts any late add under the same tag.
					setStarted(false)
					apply.clearReceipt, apply.absent = true, 0
				}
			} else {
				unknown()
			}
		}
	default:
		unknown()
	}
	match, err := findLibraryMatch(ctx, tx, r)
	if err != nil {
		return false, nil, err
	}
	newStatus, content := "approved", ""
	if match != "" {
		newStatus, content, apply.fulfilment, apply.reason = "completed", match, "library", ""
	} else if apply.providerDone {
		items, err := audiobookPathMatches(ctx, tx, r, work.input)
		if err != nil {
			return false, nil, err
		}
		switch len(items) {
		case 0:
		case 1:
			newStatus, content, apply.fulfilment, apply.reason = "completed", items[0], "library", ""
		default:
			apply.providerStatus, apply.fulfilment, apply.reason, apply.providerDone = "review", "review", audiobookReasonReview, false
			setReview("verification")
			if err = insertHostAudiobookReview(ctx, tx, r.ID, work.input.Attempt); err != nil {
				return false, nil, err
			}
		}
	}
	previous := r.Fulfilment
	if r, err = updateEbookState(ctx, tx, r, newStatus, apply.fulfilment, apply.reason, content); err != nil {
		return false, nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET provider_status=$3,failure_count=$4,started=COALESCE($5,started),external_id=CASE WHEN $6 THEN '' ELSE external_id END,
 lease_token=NULL,lease_until=NULL,next_attempt_at=now()+make_interval(secs=>$7),updated_at=now() WHERE request_id=$1 AND lease_token=$2`,
		r.ID, work.token, apply.providerStatus, apply.failures, apply.started, apply.clearReceipt, apply.delay.Seconds())
	if err != nil {
		return false, nil, err
	}
	// A repeated unreadable check keeps updated_at, so it dates the outage
	// for the admin stuck view.
	_, err = tx.Exec(ctx, `UPDATE bloem_audiobook_request_acquisitions SET absent_checks=$3,external_status=COALESCE($4,external_status),
 search_passes=search_passes+CASE WHEN $5 THEN 1 ELSE 0 END,review_kind=COALESCE($6,review_kind),
 provider_completed_at=CASE WHEN $7 THEN COALESCE(provider_completed_at,now()) ELSE provider_completed_at END,
 updated_at=CASE WHEN $8 AND external_status='unknown' THEN updated_at ELSE now() END
 WHERE request_id=$1 AND attempt=$2`, r.ID, work.input.Attempt, apply.absent, apply.externalStatus, apply.searchPass, apply.reviewKind, apply.providerDone, apply.unreadable)
	if err != nil {
		return false, nil, err
	}
	var notices []audiobookNotice
	if newStatus != "completed" && previous != apply.fulfilment && (apply.fulfilment == "review" || apply.fulfilment == "failed") {
		if notices, err = markAudiobookAttentionTx(ctx, tx, r, work.input.Attempt, attentionAttempt); err != nil {
			return false, nil, err
		}
	}
	return newStatus == "completed", notices, tx.Commit(ctx)
}

// markAudiobookAttentionTx records that this attempt's admin notice is due,
// at most once per attempt; the caller sends it after commit.
func markAudiobookAttentionTx(ctx context.Context, tx pgx.Tx, r Request, attempt, notified int) ([]audiobookNotice, error) {
	if notified >= attempt {
		return nil, nil
	}
	tag, err := tx.Exec(ctx, `UPDATE bloem_audiobook_request_acquisitions SET attention_notified_attempt=$2 WHERE request_id=$1 AND attention_notified_attempt<$2`, r.ID, attempt)
	if err != nil || tag.RowsAffected() != 1 {
		return nil, err
	}
	return []audiobookNotice{{request: r, attempt: attempt}}, nil
}

func insertHostAudiobookReview(ctx context.Context, tx pgx.Tx, requestID string, attempt int) error {
	decision := &AcquisitionDecision{V: 1, Phase: "verify", Outcome: "review", Issues: []string{"multiple_items"}}
	envelope, err := decision.Encode()
	if err != nil {
		return err
	}
	return insertAudiobookDecisionTx(ctx, tx, requestID, attempt, "verify", "review", envelope, false)
}

// completeAudiobookByPathTx handles a verified download waiting for the
// library scan: exactly one distinct audiobook item under the attempt's
// marker folder completes the request. Several items go to review; none
// past 24 hours raises one attention notice.
func (s *Service) completeAudiobookByPathTx(ctx context.Context, tx pgx.Tx, r Request, work *audiobookWork, completedAt *time.Time, attentionAttempt int) (bool, []audiobookNotice, error) {
	items, err := audiobookPathMatches(ctx, tx, r, work.input)
	if err != nil {
		return false, nil, err
	}
	switch len(items) {
	case 1:
		if _, err = updateEbookState(ctx, tx, r, "completed", "library", "", items[0]); err != nil {
			return false, nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET lease_token=NULL,lease_until=NULL,updated_at=now() WHERE request_id=$1`, r.ID)
		return err == nil, nil, err
	case 0:
		if _, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET next_attempt_at=now()+make_interval(secs=>$2) WHERE request_id=$1`, r.ID, audiobookPollDelay.Seconds()); err != nil {
			return false, nil, err
		}
		if completedAt != nil && time.Since(*completedAt) > 24*time.Hour {
			notices, err := markAudiobookAttentionTx(ctx, tx, r, work.input.Attempt, attentionAttempt)
			return false, notices, err
		}
		return false, nil, nil
	default:
		if r, err = updateEbookState(ctx, tx, r, "approved", "review", audiobookReasonReview, ""); err != nil {
			return false, nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET provider_status='review',updated_at=now() WHERE request_id=$1`, r.ID); err != nil {
			return false, nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE bloem_audiobook_request_acquisitions SET review_kind='verification',updated_at=now() WHERE request_id=$1`, r.ID); err != nil {
			return false, nil, err
		}
		if err = insertHostAudiobookReview(ctx, tx, r.ID, work.input.Attempt); err != nil {
			return false, nil, err
		}
		notices, err := markAudiobookAttentionTx(ctx, tx, r, work.input.Attempt, attentionAttempt)
		return false, notices, err
	}
}

// audiobookPathMatches returns up to two distinct audiobook items with a
// scanned file inside this attempt's marker folder under a frozen root. Range
// operators keep idx_media_files_folder_file_path_pattern usable from cached
// generic plans.
func audiobookPathMatches(ctx context.Context, tx pgx.Tx, r Request, in AudiobookDispatchInput) ([]string, error) {
	if in.Integration.LibraryID <= 0 || in.SubmissionID == "" || in.Attempt < 1 {
		return nil, nil
	}
	marker := " [" + AudiobookMarker(in.SubmissionID, in.Attempt) + "]/"
	seen := map[string]bool{}
	out := []string{}
	for _, root := range in.Integration.DownloadRoots {
		if validDownloadRoots([]string{root}, []string{root}) != nil {
			continue
		}
		rows, err := tx.Query(ctx, audiobookPathMatchSQL, in.Integration.LibraryID, root+"/", root+"0", marker, r.OrganizationID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var content string
			if err = rows.Scan(&content); err != nil {
				rows.Close()
				return nil, err
			}
			if !seen[content] {
				seen[content] = true
				out = append(out, content)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(out) > 1 {
			return out[:2], nil
		}
	}
	if len(out) == 1 {
		var locked string
		err := tx.QueryRow(ctx, `SELECT content_id FROM media_items WHERE content_id=$1 AND type='audiobook' FOR SHARE`, out[0]).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

const audiobookPathMatchSQL = `
 WITH files AS MATERIALIZED (SELECT content_id,media_folder_id,file_path FROM media_files
 WHERE media_folder_id=$1 AND missing_since IS NULL AND file_path ~>=~ $2 AND file_path ~<~ $3)
 SELECT DISTINCT i.content_id FROM files mf
 JOIN media_items i ON i.content_id=mf.content_id AND i.type='audiobook'
 JOIN media_folders f ON f.id=mf.media_folder_id AND f.enabled AND lower(btrim(f.type)) IN ('audiobook','audiobooks')
 JOIN resource_owners ro ON ro.id=f.owner_id
 WHERE strpos(mf.file_path,$4)>0
 AND ((ro.kind='organization' AND ro.organization_id=$5)
  OR (ro.kind='platform' AND EXISTS(SELECT 1 FROM organization_entitlements e WHERE e.organization_id=$5 AND e.media_folder_id=f.id AND e.root_owner_id=ro.id AND e.status='active')))
 LIMIT 2`
