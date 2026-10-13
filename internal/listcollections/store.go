package listcollections

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type storedJob struct {
	job          Job
	subject      Subject
	state, grant string
	leaseHash    []byte
	resolution   []byte
	binding      *Binding
	claimedAt    *time.Time
}

// All writers take fresh authority, then binding, then job, then native target
// rows. A plain scoped peek provides the immutable identity for those locks.
func (s *Service) lockJob(ctx context.Context, tx pgx.Tx, installation int, id string) (storedJob, error) {
	var out storedJob
	var raw []byte
	var binding *string
	if !validID(id) || installation < 1 {
		return out, ErrAuthority
	}
	err := tx.QueryRow(ctx, `SELECT subject,binding_id::text FROM bloem_list_jobs WHERE id=$1 AND installation_id=$2`, id, installation).Scan(&raw, &binding)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrAuthority
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out.subject); err != nil {
		return out, err
	}
	if out.subject.InstallationID != installation {
		return out, ErrAuthority
	}
	if err = s.authorize(ctx, tx, out.subject); err != nil {
		return out, err
	}
	if binding != nil {
		b, e := s.lockBinding(ctx, tx, *binding)
		if e != nil {
			return out, e
		}
		if b.Subject != out.subject || b.Status == "detached" {
			return out, ErrAuthority
		}
		out.binding = &b
		out.job.BindingID = *binding
	}
	err = tx.QueryRow(ctx, `SELECT id::text,mode,generation,state,deadline,lease_digest,resolution,grant_cipher,claimed_at FROM bloem_list_jobs WHERE id=$1 AND installation_id=$2 FOR UPDATE`, id, installation).Scan(&out.job.ID, &out.job.Mode, &out.job.Generation, &out.state, &out.job.ExpiresAt, &out.leaseHash, &out.resolution, &out.grant, &out.claimedAt)
	if err != nil {
		return out, err
	}
	out.job.InstanceID = out.subject.InstanceID
	out.job.AccountID = strconv.Itoa(out.subject.UserID)
	out.job.ProfileID = out.subject.ProfileID
	if out.binding != nil {
		out.job.SourceID = out.binding.SourceID
		if out.job.Generation != out.binding.Generation {
			return out, ErrConflict
		}
	}
	return out, nil
}
func (s *Service) lockBinding(ctx context.Context, tx pgx.Tx, id string) (Binding, error) {
	var out Binding
	var raw, unmatched []byte
	err := tx.QueryRow(ctx, `SELECT id::text,subject,source_id,signing_key,scope,target_id,library_ids,generation,status,last_success,matched,unmatched,failure FROM bloem_list_bindings WHERE id=$1 FOR UPDATE`, id).Scan(&out.ID, &raw, &out.SourceID, &out.SigningKey, &out.Scope, &out.TargetID, &out.LibraryIDs, &out.Generation, &out.Status, &out.LastSuccess, &out.Matched, &unmatched, &out.Failure)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrAuthority
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out.Subject); err != nil {
		return out, err
	}
	err = json.Unmarshal(unmatched, &out.Unmatched)
	return out, err
}
func checkLease(j storedJob, token string, generation uint64) error {
	if !requiredText(token, 16384) || generation != j.job.Generation {
		return ErrAuthority
	}
	hash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(j.leaseHash, hash[:]) != 1 {
		return ErrAuthority
	}
	return nil
}
func (s *Service) enqueueRefresh(ctx context.Context, tx pgx.Tx, b Binding) (Job, error) {
	var out Job
	if err := s.authorize(ctx, tx, b.Subject); err != nil {
		return out, err
	}
	// Caller already holds the binding. Expired jobs cannot block future work.
	if _, err := tx.Exec(ctx, `UPDATE bloem_list_jobs SET state='failed',failure='expired' WHERE binding_id=$1 AND state IN ('pending','running') AND deadline<=clock_timestamp()`, b.ID); err != nil {
		return out, err
	}
	var existing string
	err := tx.QueryRow(ctx, `SELECT id::text FROM bloem_list_jobs WHERE binding_id=$1 AND state IN ('pending','running')`, b.ID).Scan(&existing)
	if err == nil {
		j, e := s.lockJob(ctx, tx, b.Subject.InstallationID, existing)
		return j.job, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if err = tx.QueryRow(ctx, `UPDATE bloem_list_bindings SET generation=generation+1,status='queued',failure='' WHERE id=$1 RETURNING generation`, b.ID).Scan(&b.Generation); err != nil {
		return out, err
	}
	// First refresh also uses a new generation; no work exists at generation 1.
	out = Job{ID: uuid.NewString(), Mode: "refresh", InstanceID: b.Subject.InstanceID, AccountID: strconv.Itoa(b.Subject.UserID), ProfileID: b.Subject.ProfileID, BindingID: b.ID, SourceID: b.SourceID, Generation: b.Generation}
	raw, err := json.Marshal(b.Subject)
	if err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO bloem_list_jobs(id,installation_id,subject,mode,binding_id,generation) VALUES($1,$2,$3,'refresh',$4,$5) RETURNING deadline`, out.ID, b.Subject.InstallationID, raw, b.ID, b.Generation).Scan(&out.ExpiresAt)
	return out, err
}

func (s *Service) ResolvedTx(ctx context.Context, tx pgx.Tx, installation int, r Resolution) error {
	j, err := s.lockJob(ctx, tx, installation, r.JobID)
	if err != nil {
		return err
	}
	if j.job.Mode != "resolve" || j.state != "running" {
		return ErrConflict
	}
	if err = checkLease(j, r.LeaseToken, r.Generation); err != nil {
		return err
	}
	now, err := dbNow(ctx, tx)
	if err != nil {
		return err
	}
	if !j.job.ExpiresAt.After(now) || !r.ExpiresAt.After(now) || r.ExpiresAt.Sub(now) > SnapshotLease {
		return ErrExpired
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(r.SigningKey)
	if err != nil || len(key) != 32 || len(r.SigningKey) != 43 || !requiredText(r.SourceID, 256) || !requiredText(r.Name, 4096) || !requiredText(r.ResolutionToken, 16384) || r.Count < 0 || r.Count > MaxSnapshotItems {
		return ErrSnapshot
	}
	switch r.Kind {
	case "manual", "smart", "followed", "collaborative":
	default:
		return ErrSnapshot
	}
	grant, err := s.Cipher.Encrypt(r.ResolutionToken, "bloem.list.grant:"+j.job.ID)
	if err != nil {
		return err
	}
	r.ResolutionToken = ""
	r.LeaseToken = ""
	if r.ExpiresAt.After(j.job.ExpiresAt) {
		r.ExpiresAt = j.job.ExpiresAt
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE bloem_list_jobs SET state='resolved',resolution=$2,grant_cipher=$3,deadline=$4 WHERE id=$1`, j.job.ID, raw, grant, r.ExpiresAt)
	return err
}
