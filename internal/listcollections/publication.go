package listcollections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *Service) StageTx(ctx context.Context, tx pgx.Tx, installation int, p Page) error {
	j, err := s.lockJob(ctx, tx, installation, p.JobID)
	if err != nil {
		return err
	}
	if j.binding == nil || j.state != "running" || p.BindingID != j.binding.ID || p.SourceID != j.binding.SourceID {
		return ErrConflict
	}
	if err = checkLease(j, p.LeaseToken, p.Generation); err != nil {
		return err
	}
	now, err := dbNow(ctx, tx)
	if err != nil {
		return err
	}
	if !j.job.ExpiresAt.After(now) {
		return ErrExpired
	}
	if err = ValidatePageAt(p, now); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	// Decode only the retried row or latest header during staging. Complete
	// alone reads the full payload, avoiding repeated snapshot decryption.
	var stored string
	err = tx.QueryRow(ctx, `SELECT page_cipher FROM bloem_list_pages WHERE job_id=$1 AND position=$2`, j.job.ID, p.Position).Scan(&stored)
	if err == nil {
		prior, e := s.Cipher.Decrypt(stored, pageAAD(j.job.ID, p.Position))
		if e != nil {
			return e
		}
		if bytes.Equal([]byte(prior), raw) {
			return nil
		}
		return ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	position := 0
	var lastPosition int
	err = tx.QueryRow(ctx, `SELECT position,page_cipher FROM bloem_list_pages WHERE job_id=$1 ORDER BY position DESC LIMIT 1`, j.job.ID).Scan(&lastPosition, &stored)
	if err == nil {
		prior, e := s.Cipher.Decrypt(stored, pageAAD(j.job.ID, lastPosition))
		if e != nil {
			return e
		}
		var page Page
		if e = decodeStrict([]byte(prior), &page); e != nil {
			return e
		}
		if page.Position != lastPosition || page.Final {
			return ErrConflict
		}
		position = page.Position + len(page.Entries)
		if p.Version != page.Version || p.BindingID != page.BindingID || p.SourceID != page.SourceID || p.SnapshotID != page.SnapshotID || p.Generation != page.Generation || p.LeaseToken != page.LeaseToken || p.Total != page.Total || p.Digest != page.Digest || !p.GeneratedAt.Equal(page.GeneratedAt) || !p.ExpiresAt.Equal(page.ExpiresAt) {
			return ErrSnapshot
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var count, totalBytes int
	if err = tx.QueryRow(ctx, `SELECT count(*),COALESCE(sum(payload_bytes),0) FROM bloem_list_pages WHERE job_id=$1`, j.job.ID).Scan(&count, &totalBytes); err != nil {
		return err
	}
	if p.Position != position || count >= MaxSnapshotItems || totalBytes+len(raw) > MaxSnapshotBytes {
		return ErrSnapshot
	}
	sealed, err := s.Cipher.Encrypt(string(raw), pageAAD(j.job.ID, p.Position))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_list_pages(job_id,position,payload_bytes,page_cipher) VALUES($1,$2,$3,$4)`, j.job.ID, p.Position, len(raw), sealed)
	return err
}

func (s *Service) pagesTx(ctx context.Context, tx pgx.Tx, job string) ([]Page, error) {
	rows, err := tx.Query(ctx, `SELECT position,payload_bytes,page_cipher FROM bloem_list_pages WHERE job_id=$1 ORDER BY position`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Page{}
	total := 0
	for rows.Next() {
		var position, size int
		var sealed string
		if err = rows.Scan(&position, &size, &sealed); err != nil {
			return nil, err
		}
		total += size
		if total > MaxSnapshotBytes || len(out) >= MaxSnapshotItems {
			return nil, ErrSnapshot
		}
		raw, e := s.Cipher.Decrypt(sealed, pageAAD(job, position))
		if e != nil {
			return nil, e
		}
		if len(raw) != size {
			return nil, ErrSnapshot
		}
		var p Page
		if e = decodeStrict([]byte(raw), &p); e != nil {
			return nil, e
		}
		if p.Position != position {
			return nil, ErrSnapshot
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) CompleteTx(ctx context.Context, tx pgx.Tx, installation int, in Completion) (Receipt, error) {
	var out Receipt
	j, err := s.lockJob(ctx, tx, installation, in.JobID)
	if err != nil {
		return out, err
	}
	if j.binding == nil || in.BindingID != j.binding.ID || (j.state != "running" && j.state != "complete") {
		return out, ErrConflict
	}
	if err = checkLease(j, in.LeaseToken, in.Generation); err != nil {
		return out, err
	}
	pages, err := s.pagesTx(ctx, tx, j.job.ID)
	if err != nil {
		return out, err
	}
	now, err := dbNow(ctx, tx)
	if err != nil {
		return out, err
	}
	if !j.job.ExpiresAt.After(now) {
		return out, ErrExpired
	}
	snapshot, err := VerifySnapshot(pages, now)
	if err != nil {
		return out, err
	}
	if snapshot.ID != in.SnapshotID || snapshot.Digest != in.Digest || snapshot.BindingID != j.binding.ID || snapshot.SourceID != j.binding.SourceID || snapshot.Generation != j.binding.Generation {
		return out, ErrSnapshot
	}
	want := SourceProof{Version: 1, InstanceID: j.subject.InstanceID, AccountID: j.job.AccountID, ProfileID: j.subject.ProfileID, SourceID: snapshot.SourceID, SnapshotID: snapshot.ID, Digest: snapshot.Digest, IssuedAt: snapshot.GeneratedAt, ExpiresAt: snapshot.ExpiresAt}
	if j.claimedAt == nil {
		return out, ErrAuthority
	}
	if j.claimedAt.After(want.IssuedAt) {
		want.IssuedAt = *j.claimedAt
	}
	if err = VerifySourceProof(j.binding.SigningKey, in.SourceAttestation, want, now); err != nil {
		return out, err
	}
	var receipt, digest []byte
	var snapshotID string
	err = tx.QueryRow(ctx, `SELECT snapshot_id,digest,receipt FROM bloem_list_receipts WHERE job_id=$1`, j.job.ID).Scan(&snapshotID, &digest, &receipt)
	if err == nil {
		if snapshotID != in.SnapshotID || !bytes.Equal(digest, in.Digest[:]) {
			return out, ErrConflict
		}
		err = json.Unmarshal(receipt, &out)
		return out, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if j.state == "complete" {
		return out, ErrConflict
	}
	candidates, err := s.Domain.CandidatesTx(ctx, tx, j.subject, *j.binding, snapshot.Entries)
	if err != nil {
		return out, err
	}
	matches, err := MatchEntries(snapshot.Entries, candidates)
	if err != nil {
		return out, err
	}
	revision, err := s.Domain.ReplaceTx(ctx, tx, j.subject, *j.binding, matches.ContentIDs)
	if err != nil {
		return out, err
	}
	if revision < 1 {
		return out, ErrSnapshot
	}
	if err = s.recordIntents(ctx, tx, *j.binding, matches.Unmatched); err != nil {
		return out, err
	}
	unmatched, err := json.Marshal(matches.Unmatched)
	if err != nil {
		return out, err
	}
	// Domain or intent locks may have waited. Neither an expired source proof nor
	// a changed profile is allowed to commit after that wait.
	if err = s.authorize(ctx, tx, j.subject); err != nil {
		return out, err
	}
	now, err = dbNow(ctx, tx)
	if err != nil {
		return out, err
	}
	if !j.job.ExpiresAt.After(now) {
		return out, ErrExpired
	}
	if err = VerifySourceProof(j.binding.SigningKey, in.SourceAttestation, want, now); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_list_bindings SET status='ready',last_success=$2::timestamptz,next_due=$2::timestamptz+interval '1 day',applied_digest=$3,matched=$4,unmatched=$5,failure='' WHERE id=$1`, j.binding.ID, now, snapshot.Digest[:], len(matches.ContentIDs), unmatched); err != nil {
		return out, err
	}
	out = Receipt{JobID: j.job.ID, BindingID: j.binding.ID, TargetID: j.binding.TargetID, Generation: j.binding.Generation, Revision: uint64(revision), Digest: in.Digest}
	receipt, err = json.Marshal(out)
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO bloem_list_receipts(job_id,snapshot_id,digest,receipt) VALUES($1,$2,$3,$4)`, j.job.ID, in.SnapshotID, in.Digest[:], receipt); err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `UPDATE bloem_list_jobs SET state='complete' WHERE id=$1`, j.job.ID)
	return out, err
}

func (s *Service) recordIntents(ctx context.Context, tx pgx.Tx, b Binding, unmatched []Unmatched) error {
	if _, err := tx.Exec(ctx, `UPDATE bloem_list_request_intents SET wanted=false,status=CASE WHEN status='delivered' THEN status ELSE 'cancelled' END WHERE binding_id=$1`, b.ID); err != nil {
		return err
	}
	for _, item := range unmatched {
		if item.Reason != "missing" {
			continue
		}
		identity, err := Digest([]Entry{{Kind: item.Entry.Kind, ExternalIDs: item.Entry.ExternalIDs}})
		if err != nil {
			return err
		}
		raw, err := json.Marshal(item.Entry)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO bloem_list_request_intents(binding_id,identity_key,entry,generation,wanted) VALUES($1,$2,$3,$4,true)
 ON CONFLICT(binding_id,identity_key) DO UPDATE SET entry=EXCLUDED.entry,generation=EXCLUDED.generation,wanted=true,status=CASE WHEN bloem_list_request_intents.status='delivered' THEN 'delivered' ELSE 'pending' END`, b.ID, identity.String(), raw, b.Generation)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RejectTx(ctx context.Context, tx pgx.Tx, installation int, id, token string, generation uint64, reason string) error {
	switch reason {
	case "invalid_snapshot", "expired", "unsupported", "denied", "unavailable":
	default:
		return ErrSnapshot
	}
	j, err := s.lockJob(ctx, tx, installation, id)
	if err != nil {
		return err
	}
	if err = checkLease(j, token, generation); err != nil {
		return err
	}
	if j.state == "complete" || j.state == "resolved" {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_list_jobs SET state='failed',failure=$2 WHERE id=$1`, id, reason); err != nil {
		return err
	}
	if j.binding != nil {
		status := "failed"
		if reason == "denied" || reason == "unsupported" {
			status = "paused"
		}
		_, err = tx.Exec(ctx, `UPDATE bloem_list_bindings SET status=$2,failure=$3,next_due=clock_timestamp()+interval '1 minute' WHERE id=$1`, j.binding.ID, status, reason)
	}
	return err
}

func (s *Service) Sweep(ctx context.Context, limit int) error {
	if s == nil || s.Pool == nil || limit < 1 || limit > 500 {
		return ErrSnapshot
	}
	if err := s.CleanupTargets(ctx, limit); err != nil {
		return err
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM bloem_list_jobs WHERE id IN (
 SELECT j.id FROM bloem_list_jobs j WHERE j.deadline<clock_timestamp()-interval '1 hour'
 AND NOT EXISTS(SELECT 1 FROM bloem_list_bindings b WHERE b.preview_id=j.id)
 ORDER BY j.deadline,j.id LIMIT $1 FOR UPDATE SKIP LOCKED)`, limit)
	return err
}
