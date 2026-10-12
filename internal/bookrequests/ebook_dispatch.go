package bookrequests

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const ebookLeaseSeconds = 90
const ebookMaxFailures = 5

// The public descriptor hides internal identities from JSON responses. This
// representation retains the same secret-free fields in the durable dispatch.
type ebookIntegrationSnapshot struct {
	ID                           string
	Name                         string
	LibraryID                    int
	OrganizationID               uuid.UUID
	InstallationID               int
	CapabilityID                 string
	ConfigurationRevision        string
	BindingRevision              int64
	StorageSourceKey             uuid.UUID
	StorageConfigurationRevision int64
}

type ebookWork struct {
	input       DispatchInput
	token       uuid.UUID
	until       time.Time
	started     bool
	receipt     Receipt
	failures    int
	policyEpoch int64
}

func ebookAcquisitionEnabled(ctx context.Context, tx pgx.Tx) error {
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM server_settings WHERE key=$1 AND value='true' FOR SHARE) AND EXISTS(SELECT 1 FROM request_settings WHERE id=true AND requests_enabled FOR SHARE)`, config.EbookRequestsEnabledSettingKey).Scan(&enabled)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	return nil
}

func validProviderStatus(status string) bool {
	switch status {
	case "unknown", "queued", "searching", "downloading", "completed", "failed":
		return true
	}
	return false
}

// ReconcileEbooks bounds each batch and leases work in PostgreSQL. Provider
// calls run only after commit; a persisted started marker makes an expired or
// ambiguous submission recover through reads rather than another create call.
func (s *Service) ReconcileEbooks(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id FROM bloem_book_requests r LEFT JOIN bloem_ebook_request_dispatches d ON d.request_id=r.id
 WHERE r.media_type='book' AND r.outcome='active' AND r.status='approved'
 AND (d.request_id IS NULL OR (d.failure_count<$2 AND d.next_attempt_at<=now() AND (d.lease_until IS NULL OR d.lease_until<=now())))
 ORDER BY r.last_checked_at NULLS FIRST,r.created_at,r.id LIMIT $1`, limit, ebookMaxFailures)
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
	completed := 0
	for _, id := range ids {
		work, done, err := s.claimEbook(ctx, id)
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
		callCtx, cancel := context.WithDeadline(ctx, work.until.Add(-5*time.Second))
		var receipt Receipt
		var status ProviderStatus
		var callErr error
		switch {
		case work.receipt.Status == "completed":
			status.Status = "completed"
		case s.ebookFulfillment == nil:
			callErr = ErrDisabled
		case work.receipt.ExternalID != "":
			callErr = s.validateEbookWork(callCtx, work)
			if callErr == nil {
				status, callErr = s.ebookFulfillment.CheckStatus(callCtx, work.input, work.receipt)
			}
		case work.started:
			callErr = s.validateEbookWork(callCtx, work)
			if callErr == nil {
				receipt, callErr = s.ebookFulfillment.Recover(callCtx, work.input)
			}
		default:
			// Recheck opt-in, binding and the current requester just before the
			// RPC, without keeping a database transaction open across the call.
			callErr = s.validateEbookWork(callCtx, work)
			if callErr == nil {
				receipt, callErr = s.ebookFulfillment.Fulfill(callCtx, work.input)
			} else {
				callErr = errors.Join(ErrProviderNotCalled, callErr)
			}
		}
		cancel()
		if receipt.ExternalID != "" {
			if len(receipt.ExternalID) > 8192 || !utf8.ValidString(receipt.ExternalID) || strings.ContainsRune(receipt.ExternalID, 0) || !validProviderStatus(receipt.Status) {
				callErr = ErrInvalid
			} else {
				// A provider side effect may outlive opt-out/cancellation or this
				// lease. Save its immutable receipt before applying the outcome
				// fence, so later recovery cannot forget the accepted submission.
				saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				err = s.saveEbookReceipt(saveCtx, work, receipt)
				saveCancel()
				if err != nil {
					return completed, err
				}
				status.Status = receipt.Status
			}
		}
		if !validProviderStatus(status.Status) && callErr == nil {
			callErr = ErrInvalid
		}
		done, err = s.finishEbook(ctx, work, status, callErr)
		if err != nil {
			return completed, err
		}
		if done {
			completed++
		}
	}
	if err := s.notifyEbookCompletions(ctx, limit); err != nil {
		return completed, err
	}
	return completed, nil
}

func (s *Service) claimEbook(ctx context.Context, id string) (*ebookWork, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	r, err := scan(tx.QueryRow(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE id=$1 AND media_type='book' AND outcome='active' AND status='approved' FOR UPDATE SKIP LOCKED`, id))
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_book_requests SET last_checked_at=now() WHERE id=$1`, id); err != nil {
		return nil, false, err
	}
	v := Viewer{UserID: r.RequestedByUserID, ProfileID: r.RequestedByProfileID, OrganizationID: r.OrganizationID}
	if err = checkEbookAuthority(ctx, tx, v, true); err != nil {
		if errors.Is(err, ErrDisabled) || errors.Is(err, ErrForbidden) {
			return nil, false, tx.Commit(ctx)
		}
		return nil, false, err
	}
	if err = ebookAcquisitionEnabled(ctx, tx); err != nil {
		if errors.Is(err, ErrDisabled) {
			return nil, false, tx.Commit(ctx)
		}
		return nil, false, err
	}
	work := &ebookWork{input: DispatchInput{Request: r, IdempotencyKey: r.ID}, token: uuid.New()}
	var descriptor []byte
	var leaseUntil *time.Time
	var next time.Time
	err = tx.QueryRow(ctx, `SELECT descriptor,started,external_id,provider_status,failure_count,lease_until,next_attempt_at,submission_request_id FROM bloem_ebook_request_dispatches WHERE request_id=$1 FOR UPDATE`, id).Scan(&descriptor, &work.started, &work.receipt.ExternalID, &work.receipt.Status, &work.failures, &leaseUntil, &next, &work.input.IdempotencyKey)
	if err == nil {
		if work.failures >= ebookMaxFailures || next.After(time.Now()) || leaseUntil != nil && leaseUntil.After(time.Now()) {
			return nil, false, tx.Commit(ctx)
		}
		var frozen ebookIntegrationSnapshot
		if err = json.Unmarshal(descriptor, &frozen); err != nil {
			return nil, false, err
		}
		work.input.Integration = EbookIntegration(frozen)
		current, checkErr := s.loadEbookIntegration(ctx, tx, r.OrganizationID, work.input.Integration.ID)
		if checkErr != nil || current != work.input.Integration {
			if checkErr != nil && !errors.Is(checkErr, ErrInvalid) && !errors.Is(checkErr, ErrConflict) {
				return nil, false, checkErr
			}
			_, err = updateEbookState(ctx, tx, r, "approved", "failed", "The backend or linked library changed; acquisition cannot resume safely.", "")
			if err != nil {
				return nil, false, err
			}
			_, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET lease_token=NULL,lease_until=NULL,failure_count=$2,updated_at=now() WHERE request_id=$1`, id, ebookMaxFailures)
			if err != nil {
				return nil, false, err
			}
			return nil, false, tx.Commit(ctx)
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		work.input.Integration, err = s.selectEbookIntegration(ctx, tx, r.OrganizationID)
		if errors.Is(err, ErrConflict) {
			return nil, false, tx.Commit(ctx)
		}
		if err != nil {
			return nil, false, err
		}
		// Canonical ISBN and backend identify a provider acquisition across
		// cancelled/refiled requests. The transaction lock serializes the
		// initial started marker across hosts, before any provider I/O.
		fence := r.OrganizationID.String() + ":" + work.input.Integration.ID + ":" + r.ISBN
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fence); err != nil {
			return nil, false, err
		}
		current := work.input.Integration
		work.receipt.Status = "unknown"
		err = tx.QueryRow(ctx, `SELECT d.descriptor,d.submission_request_id,d.external_id,d.provider_status FROM bloem_ebook_request_dispatches d JOIN bloem_book_requests prior ON prior.id=d.request_id WHERE prior.organization_id=$1 AND prior.media_type='book' AND prior.isbn=$2 AND d.integration_id=$3 AND d.started ORDER BY d.created_at,d.request_id LIMIT 1 FOR SHARE OF d`, r.OrganizationID, r.ISBN, current.ID).Scan(&descriptor, &work.input.IdempotencyKey, &work.receipt.ExternalID, &work.receipt.Status)
		if err == nil {
			work.started = true
			var frozen ebookIntegrationSnapshot
			if err = json.Unmarshal(descriptor, &frozen); err != nil {
				return nil, false, err
			}
			work.input.Integration = EbookIntegration(frozen)
		} else if errors.Is(err, pgx.ErrNoRows) {
			descriptor, err = json.Marshal(ebookIntegrationSnapshot(current))
		}
		if err != nil {
			return nil, false, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO bloem_ebook_request_dispatches(request_id,integration_id,submission_request_id,descriptor,started,external_id,provider_status) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, current.ID, work.input.IdempotencyKey, descriptor, work.started, work.receipt.ExternalID, work.receipt.Status)
		if err != nil {
			return nil, false, err
		}
		if current != work.input.Integration {
			if _, err = updateEbookState(ctx, tx, r, "approved", "failed", "The backend or linked library changed; the earlier submission remains uncertain.", ""); err != nil {
				return nil, false, err
			}
			if _, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET failure_count=$2 WHERE request_id=$1`, id, ebookMaxFailures); err != nil {
				return nil, false, err
			}
			return nil, false, tx.Commit(ctx)
		}
	} else {
		return nil, false, err
	}
	// Policy and binding rows lock before the epoch, matching observer writers.
	if err = tx.QueryRow(ctx, `SELECT revision FROM bloem_ebook_request_policy_epoch WHERE id FOR SHARE`).Scan(&work.policyEpoch); err != nil {
		return nil, false, err
	}
	match, err := findEbookLibraryMatch(ctx, tx, r, work.input.Integration)
	if err != nil {
		return nil, false, err
	}
	if match != "" {
		_, err = updateEbookState(ctx, tx, r, "completed", "library", "", match)
		if err != nil {
			return nil, false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET lease_token=NULL,lease_until=NULL WHERE request_id=$1`, id); err != nil {
			return nil, false, err
		}
		return nil, true, tx.Commit(ctx)
	}
	// Do not mark a submission started if no composed host can execute it.
	if s.ebookFulfillment == nil && work.receipt.ExternalID == "" {
		return nil, false, tx.Commit(ctx)
	}
	err = tx.QueryRow(ctx, `UPDATE bloem_ebook_request_dispatches SET started=true,lease_token=$2,lease_until=now()+make_interval(secs=>$3),updated_at=now() WHERE request_id=$1 RETURNING lease_until`, id, work.token, ebookLeaseSeconds).Scan(&work.until)
	if err != nil {
		return nil, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return work, false, nil
}

func (s *Service) validateEbookWork(ctx context.Context, work *ebookWork) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	return s.checkEbookWorkTx(ctx, tx, work)
}

func (s *Service) retryEbookTx(ctx context.Context, tx pgx.Tx, r Request) error {
	if err := ebookAcquisitionEnabled(ctx, tx); err != nil {
		return err
	}
	var descriptor []byte
	err := tx.QueryRow(ctx, `SELECT descriptor FROM bloem_ebook_request_dispatches WHERE request_id=$1 FOR UPDATE`, r.ID).Scan(&descriptor)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	var frozen ebookIntegrationSnapshot
	if err = json.Unmarshal(descriptor, &frozen); err != nil {
		return err
	}
	current, err := s.loadEbookIntegration(ctx, tx, r.OrganizationID, frozen.ID)
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	if current != EbookIntegration(frozen) {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET failure_count=0,lease_token=NULL,lease_until=NULL,next_attempt_at=now(),updated_at=now() WHERE request_id=$1`, r.ID)
	return err
}

func (s *Service) checkEbookWorkTx(ctx context.Context, tx pgx.Tx, work *ebookWork) error {
	r := work.input.Request
	var live bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_book_requests r JOIN bloem_ebook_request_dispatches d ON d.request_id=r.id WHERE r.id=$1 AND r.outcome='active' AND r.status='approved' AND d.lease_token=$2 AND d.lease_until>now())`, r.ID, work.token).Scan(&live)
	if err != nil {
		return err
	}
	if !live {
		return ErrConflict
	}
	if err = checkEbookAuthority(ctx, tx, Viewer{UserID: r.RequestedByUserID, ProfileID: r.RequestedByProfileID, OrganizationID: r.OrganizationID}, true); err != nil {
		return err
	}
	if err = ebookAcquisitionEnabled(ctx, tx); err != nil {
		return err
	}
	current, err := s.loadEbookIntegration(ctx, tx, r.OrganizationID, work.input.Integration.ID)
	if err != nil {
		return err
	}
	if current != work.input.Integration {
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

func (s *Service) saveEbookReceipt(ctx context.Context, work *ebookWork, receipt Receipt) error {
	descriptor, err := json.Marshal(ebookIntegrationSnapshot(work.input.Integration))
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET external_id=$2,updated_at=now() WHERE request_id=$1 AND descriptor=$3::jsonb AND (external_id='' OR external_id=$2)`, work.input.Request.ID, receipt.ExternalID, descriptor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Service) finishEbook(ctx context.Context, work *ebookWork, status ProviderStatus, callErr error) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	r, err := scan(tx.QueryRow(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE id=$1 FOR UPDATE`, work.input.Request.ID))
	if err != nil {
		return false, err
	}
	var token *uuid.UUID
	var until *time.Time
	if err = tx.QueryRow(ctx, `SELECT lease_token,lease_until FROM bloem_ebook_request_dispatches WHERE request_id=$1 FOR UPDATE`, r.ID).Scan(&token, &until); err != nil {
		return false, err
	}
	if token == nil || *token != work.token || until == nil || !until.After(time.Now()) {
		return false, nil
	}
	// Reset only a new, receipt-free claim whose adapter proved no submission.
	// An adopted or recovered acquisition always retains its started fence.
	if errors.Is(callErr, ErrProviderNotCalled) && !work.started && work.receipt.ExternalID == "" {
		if _, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET started=false WHERE request_id=$1 AND lease_token=$2 AND external_id=''`, r.ID, work.token); err != nil {
			return false, err
		}
	}
	if err = s.checkEbookWorkTx(ctx, tx, work); err != nil {
		if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrDisabled) && !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrInvalid) {
			return false, err
		}
		_, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET lease_token=NULL,lease_until=NULL,next_attempt_at=now()+interval '30 seconds' WHERE request_id=$1 AND lease_token=$2`, r.ID, work.token)
		if err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	fulfilment, reason, failures, delay := "queued", "", 0, 30
	providerStatus := status.Status
	if callErr != nil || status.Status == "unknown" {
		failures = work.failures + 1
		delay = min(900, 30*(1<<min(failures-1, 5)))
		fulfilment, reason = "retrying", "Submission outcome is unknown; checking the backend without submitting again."
		providerStatus = "unknown"
	} else {
		switch status.Status {
		case "searching", "downloading":
			fulfilment = "searching"
		case "completed":
			fulfilment = "awaiting_library"
		case "failed":
			failures = ebookMaxFailures
			fulfilment, reason = "failed", "The backend could not fulfill this request; retry checks the existing submission."
		}
	}
	if failures >= ebookMaxFailures {
		fulfilment = "failed"
		if reason == "" {
			reason = "Backend checks stopped after repeated failures; retry checks the existing submission."
		}
	}
	match, err := findEbookLibraryMatch(ctx, tx, r, work.input.Integration)
	if err != nil {
		return false, err
	}
	done := match != ""
	newStatus := "approved"
	if done {
		newStatus, fulfilment, reason = "completed", "library", ""
	}
	if _, err = updateEbookState(ctx, tx, r, newStatus, fulfilment, reason, match); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE bloem_ebook_request_dispatches SET provider_status=$3,failure_count=$4,lease_token=NULL,lease_until=NULL,next_attempt_at=now()+make_interval(secs=>$5),updated_at=now() WHERE request_id=$1 AND lease_token=$2`, r.ID, work.token, providerStatus, failures, delay)
	if err != nil {
		return false, err
	}
	return done, tx.Commit(ctx)
}

func updateEbookState(ctx context.Context, tx pgx.Tx, r Request, status, fulfilment, reason, content string) (Request, error) {
	if r.Status == status && r.Fulfilment == fulfilment && r.OutcomeReason == reason && r.LibraryContentID == content {
		return r, nil
	}
	return scan(tx.QueryRow(ctx, `UPDATE bloem_book_requests SET status=$2,fulfilment=$3,outcome_reason=$4,library_content_id=$5,completed_at=CASE WHEN $2='completed' THEN now() ELSE NULL END,revision=revision+1,updated_at=now() WHERE id=$1 RETURNING `+selectColumns, r.ID, status, fulfilment, reason, content))
}

func findEbookLibraryMatch(ctx context.Context, tx pgx.Tx, r Request, in EbookIntegration) (string, error) {
	rows, err := tx.Query(ctx, `
 SELECT i.content_id,mf.file_path,l.id,ref.entry_id FROM media_items i
 JOIN media_files mf ON mf.content_id=i.content_id AND mf.media_folder_id=$2 AND mf.missing_since IS NULL AND mf.container IN ('epub','pdf') AND mf.probe_source='storage'
 JOIN bloem_storage_file_refs ref ON ref.media_file_id=mf.id
 JOIN library_storage_locations l ON l.id=ref.location_id AND l.folder_id=mf.media_folder_id AND l.source_key=$3
 JOIN bloem_storage_sources s ON s.key=l.source_key AND s.enabled AND s.configuration_revision=$4
 JOIN bloem_storage_entries e ON e.source_key=s.key AND e.entry_id=ref.entry_id AND e.kind=1 AND e.absence_confirmed_at IS NULL
  AND e.revision=ref.revision AND e.logical_path=ref.logical_path AND e.configuration_revision=s.configuration_revision AND ref.configuration_revision=s.configuration_revision
 WHERE i.type='ebook' AND EXISTS(SELECT 1 FROM media_item_provider_ids ids WHERE ids.content_id=i.content_id AND ids.item_type='ebook' AND lower(ids.provider)='isbn' AND upper(regexp_replace(ids.provider_id,'[-[:space:]]','','g'))=ANY($1) FOR SHARE)
	ORDER BY i.content_id,mf.id LIMIT 100 FOR SHARE OF i,mf,ref,l,s,e`, isbnVariants(r.ISBN), in.LibraryID, in.StorageSourceKey, in.StorageConfigurationRevision)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var content, path, entry string
		var location uuid.UUID
		if err = rows.Scan(&content, &path, &location, &entry); err != nil {
			return "", err
		}
		expected, err := storagesource.CatalogLocation(location, entry)
		if err == nil && path == expected {
			return content, nil
		}
	}
	return "", rows.Err()
}

func (s *Service) notifyEbookCompletions(ctx context.Context, limit int) error {
	if s.notifier == nil {
		return nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE media_type='book' AND status='completed' AND outcome='active' AND fulfilled_notified_at IS NULL ORDER BY completed_at,id LIMIT $1`, limit)
	if err != nil {
		return err
	}
	requests := []Request{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			rows.Close()
			return err
		}
		requests = append(requests, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range requests {
		if err = s.notifier.Fulfilled(ctx, r); err != nil {
			return err
		}
		tag, err := s.pool.Exec(ctx, `UPDATE bloem_book_requests SET fulfilled_notified_at=now() WHERE id=$1 AND fulfilled_notified_at IS NULL`, r.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			if n, ok := s.notifier.(interface {
				AnnounceFulfilled(context.Context, Request)
			}); ok {
				n.AnnounceFulfilled(ctx, r)
			}
		}
	}
	return nil
}
