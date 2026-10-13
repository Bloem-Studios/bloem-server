package listcollections

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/secret"
)

var ErrAuthority = errors.New("list collection authority denied")
var ErrConflict = errors.New("list collection conflict")
var ErrExpired = errors.New("list collection lease expired")

// Subject comes from host authentication, never client JSON. AuthorizeTx must
// resolve and fence fresh installation, enrollment, account and profile policy.
type Subject struct {
	UserID         int
	InstallationID int
	ProfileID      string
	InstanceID     string
	IsAdmin        bool
}
type PreviewInput struct{ URL string }
type CreateInput struct {
	PreviewID, Scope, Name string
	IsShared               bool
	LibraryIDs             []int
}
type Resolution struct {
	JobID, LeaseToken, SourceID, Name, Kind, ResolutionToken, SigningKey string
	Generation                                                           uint64
	Count                                                                int
	ExpiresAt                                                            time.Time
}
type Binding struct {
	ID, SourceID, TargetID, Scope, Status string
	Subject                               Subject `json:"-"`
	SigningKey                            string  `json:"-"`
	Generation                            uint64
	LibraryIDs                            []int
	LastSuccess                           *time.Time
	Matched                               int
	Unmatched                             []Unmatched
	Failure                               string
}
type CollectionDomain interface {
	AuthorizeTx(context.Context, pgx.Tx, Subject) error
	AuthorizeDetachTx(context.Context, pgx.Tx, Subject) error
	CreateTx(context.Context, pgx.Tx, Subject, CreateInput, string) (string, int64, error)
	CandidatesTx(context.Context, pgx.Tx, Subject, Binding, []Entry) ([]Candidate, error)
	ReplaceTx(context.Context, pgx.Tx, Subject, Binding, []string) (int64, error)
	DisconnectTx(context.Context, pgx.Tx, Subject, Binding) error
}
type Service struct {
	Pool     *pgxpool.Pool
	Cipher   *secret.Cipher
	Domain   CollectionDomain
	Requests RequestDomain
}

func (s *Service) authorize(ctx context.Context, tx pgx.Tx, subject Subject) error {
	if s == nil || s.Cipher == nil || s.Domain == nil || tx == nil || subject.UserID < 1 || subject.InstallationID < 1 || !requiredText(subject.ProfileID, 256) || !requiredText(subject.InstanceID, 256) {
		return ErrAuthority
	}
	return s.Domain.AuthorizeTx(ctx, tx, subject)
}
func (s *Service) transaction(ctx context.Context, subject Subject, fn func(pgx.Tx) error) error {
	return s.transactionAuthorized(ctx, subject, s.authorize, fn)
}
func (s *Service) transactionAuthorized(ctx context.Context, subject Subject, authorize func(context.Context, pgx.Tx, Subject) error, fn func(pgx.Tx) error) error {
	if s == nil || s.Pool == nil {
		return ErrAuthority
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = LaneTx(ctx, tx, subject.InstallationID); err != nil {
		return err
	}
	if err = authorize(ctx, tx, subject); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = authorize(ctx, tx, subject); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func dbNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, err
}
func validID(id string) bool { _, err := uuid.Parse(id); return err == nil }
func (s *Service) QueuePreview(ctx context.Context, subject Subject, in PreviewInput) (Job, error) {
	var out Job
	parsed, err := url.Parse(in.URL)
	if err != nil || len(in.URL) > 4096 || !requiredText(in.URL, 4096) || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return out, ErrSnapshot
	}
	err = s.transaction(ctx, subject, func(tx pgx.Tx) error {
		// Bound queued previews per profile; serialize admission before counting.
		if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "bloem.list.preview:"+strconv.Itoa(subject.InstallationID)+":"+strconv.Itoa(subject.UserID)+":"+subject.ProfileID); e != nil {
			return e
		}
		var count int
		if e := tx.QueryRow(ctx, `SELECT count(*) FROM bloem_list_jobs WHERE installation_id=$1 AND subject->>'UserID'=$2 AND subject->>'ProfileID'=$3 AND mode='resolve' AND state IN ('pending','running','resolved') AND deadline>clock_timestamp()`, subject.InstallationID, strconv.Itoa(subject.UserID), subject.ProfileID).Scan(&count); e != nil {
			return e
		}
		if count >= 8 {
			return ErrConflict
		}
		out = Job{ID: uuid.NewString(), Mode: "resolve", InstanceID: subject.InstanceID, AccountID: strconv.Itoa(subject.UserID), ProfileID: subject.ProfileID, Generation: 1}
		locator, e := s.Cipher.Encrypt(in.URL, "bloem.list.locator:"+out.ID)
		if e != nil {
			return e
		}
		raw, e := json.Marshal(subject)
		if e != nil {
			return e
		}
		return tx.QueryRow(ctx, `INSERT INTO bloem_list_jobs(id,installation_id,subject,mode,generation,locator_cipher) VALUES($1,$2,$3,'resolve',1,$4) RETURNING deadline`, out.ID, subject.InstallationID, raw, locator).Scan(&out.ExpiresAt)
	})
	return out, err
}
func (s *Service) CreateBinding(ctx context.Context, subject Subject, in CreateInput) (Binding, error) {
	var out Binding
	if in.LibraryIDs == nil {
		in.LibraryIDs = []int{}
	}
	for _, id := range in.LibraryIDs {
		if id < 1 {
			return out, ErrSnapshot
		}
	}
	if !validID(in.PreviewID) || !requiredText(in.Name, 200) || (in.Scope != "personal" && in.Scope != "server") || len(in.LibraryIDs) > 1000 || in.Scope == "server" && (!subject.IsAdmin || in.IsShared || len(in.LibraryIDs) == 0) || in.Scope == "personal" && len(in.LibraryIDs) != 0 {
		return out, ErrSnapshot
	}
	err := s.transaction(ctx, subject, func(tx pgx.Tx) error {
		j, e := s.lockJob(ctx, tx, subject.InstallationID, in.PreviewID)
		if e != nil {
			return e
		}
		if j.subject != subject || j.job.Mode != "resolve" {
			return ErrAuthority
		}
		definition, e := json.Marshal(in)
		if e != nil {
			return e
		}
		digest := sha256.Sum256(definition)
		// The preview lock serializes creation before the unique receipt exists.
		var existing string
		var priorDigest []byte
		e = tx.QueryRow(ctx, `SELECT id::text,create_digest FROM bloem_list_bindings WHERE preview_id=$1`, in.PreviewID).Scan(&existing, &priorDigest)
		if e == nil {
			if !bytes.Equal(priorDigest, digest[:]) {
				return ErrConflict
			}
			out, e = s.lockBinding(ctx, tx, existing)
			return e
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		now, e := dbNow(ctx, tx)
		if e != nil {
			return e
		}
		if j.state != "resolved" || !j.job.ExpiresAt.After(now) {
			return ErrExpired
		}
		var r Resolution
		if e = json.Unmarshal(j.resolution, &r); e != nil {
			return e
		}
		out = Binding{ID: uuid.NewString(), SourceID: r.SourceID, Scope: in.Scope, Subject: subject, SigningKey: r.SigningKey, Generation: 1, LibraryIDs: in.LibraryIDs, Status: "queued"}
		out.TargetID, _, e = s.Domain.CreateTx(ctx, tx, subject, in, out.ID)
		if e != nil {
			return e
		}
		if !requiredText(out.TargetID, 256) {
			return ErrSnapshot
		}
		grant, e := s.Cipher.Decrypt(j.grant, "bloem.list.grant:"+j.job.ID)
		if e != nil {
			return e
		}
		grant, e = s.Cipher.Encrypt(grant, "bloem.list.grant:"+out.ID)
		if e != nil {
			return e
		}
		raw, e := json.Marshal(subject)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO bloem_list_bindings(id,installation_id,user_id,profile_id,subject,preview_id,source_id,signing_key,grant_cipher,scope,target_id,library_ids,create_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, out.ID, subject.InstallationID, subject.UserID, subject.ProfileID, raw, in.PreviewID, out.SourceID, out.SigningKey, grant, in.Scope, out.TargetID, in.LibraryIDs, digest[:]); e != nil {
			return e
		}
		job, e := s.enqueueRefresh(ctx, tx, out)
		if e == nil {
			out.Generation = job.Generation
		}
		return e
	})
	return out, err
}

func (s *Service) Refresh(ctx context.Context, subject Subject, id string) (Job, error) {
	var out Job
	if !validID(id) {
		return out, ErrSnapshot
	}
	err := s.transaction(ctx, subject, func(tx pgx.Tx) error {
		b, e := s.lockBinding(ctx, tx, id)
		if e != nil {
			return e
		}
		if b.Subject != subject || b.Status == "detached" {
			return ErrAuthority
		}
		out, e = s.enqueueRefresh(ctx, tx, b)
		return e
	})
	return out, err
}
func (s *Service) Disconnect(ctx context.Context, subject Subject, id string) error {
	if !validID(id) {
		return ErrSnapshot
	}
	if s == nil || s.Domain == nil || subject.UserID < 1 || subject.InstallationID < 1 || !requiredText(subject.ProfileID, 256) || !requiredText(subject.InstanceID, 256) {
		return ErrAuthority
	}
	return s.transactionAuthorized(ctx, subject, s.Domain.AuthorizeDetachTx, func(tx pgx.Tx) error {
		b, e := s.lockBinding(ctx, tx, id)
		if e != nil {
			return e
		}
		owner := b.Subject
		owner.IsAdmin = subject.IsAdmin
		if owner != subject || b.Scope == "server" && !subject.IsAdmin {
			return ErrAuthority
		}
		if b.Status == "detached" {
			return nil
		}
		if e = s.Domain.DisconnectTx(ctx, tx, subject, b); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE bloem_list_bindings SET generation=generation+1,status='detached',grant_cipher='' WHERE id=$1`, id); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE bloem_list_jobs SET state='failed',failure='detached' WHERE binding_id=$1 AND state IN ('pending','running')`, id); e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `UPDATE bloem_list_request_intents SET wanted=false,status=CASE WHEN status='delivered' THEN status ELSE 'cancelled' END WHERE binding_id=$1`, id)
		return e
	})
}
