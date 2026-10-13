//go:build integration

package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/listcollections"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

type listPublicationDomain struct {
	fail          bool
	candidates    []listcollections.Candidate
	deniedProfile string
}

func (d *listPublicationDomain) AuthorizeTx(_ context.Context, _ pgx.Tx, s listcollections.Subject) error {
	if s.ProfileID == d.deniedProfile {
		return listcollections.ErrAuthority
	}
	return nil
}
func (d *listPublicationDomain) AuthorizeDetachTx(context.Context, pgx.Tx, listcollections.Subject) error {
	return nil
}
func (d *listPublicationDomain) CreateTx(ctx context.Context, tx pgx.Tx, s listcollections.Subject, in listcollections.CreateInput, binding string) (string, int64, error) {
	provider := pgstore.NewBloemListCollectionCreator(nil, tx, s.UserID, binding)
	handler := handlers.NewCollectionHandler(provider)
	view, err := handler.CreatePersonalCollection(ctx, handlers.PersonalCollectionCreateCommand{UserID: s.UserID, ProfileID: s.ProfileID, Request: handlers.PersonalCollectionCreateRequest{Name: in.Name, CollectionType: "synced", IsShared: in.IsShared}})
	return view.ID, 1, err
}
func (d *listPublicationDomain) CandidatesTx(context.Context, pgx.Tx, listcollections.Subject, listcollections.Binding, []listcollections.Entry) ([]listcollections.Candidate, error) {
	return d.candidates, nil
}
func (d *listPublicationDomain) ReplaceTx(ctx context.Context, tx pgx.Tx, s listcollections.Subject, b listcollections.Binding, ids []string) (int64, error) {
	rev, err := pgstore.BloemReplaceListCollectionItemsTx(ctx, tx, s.UserID, s.ProfileID, b.TargetID, b.ID, ids)
	if err == nil && d.fail {
		return 0, errors.New("synthetic publication failure")
	}
	return rev, err
}
func (d *listPublicationDomain) DisconnectTx(ctx context.Context, tx pgx.Tx, s listcollections.Subject, b listcollections.Binding) error {
	_, err := tx.Exec(ctx, `UPDATE user_personal_collections SET collection_type='manual',source_config=source_config-'bloem_list_binding' WHERE user_id=$1 AND id=$2`, s.UserID, b.TargetID)
	return err
}

func listPublicationFixture(t *testing.T) (*listcollections.Service, listcollections.Subject, *listPublicationDomain) {
	t.Helper()
	pool := coverRecipeDatabase(t)
	ctx := t.Context()
	var user, install int
	var tenant string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM organizations WHERE is_default`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,enabled) VALUES($1,'synthetic','user',true) RETURNING id`, uuid.NewString()).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO plugin_installations(plugin_id,version,install_path,enabled) VALUES('bloem.pastime','fixture','synthetic',true) RETURNING id`).Scan(&install); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bloem_managed_grants(installation_id,tenant_id) VALUES($1,$2)`, install, tenant); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title) VALUES($1,'movie','Synthetic matched')`, id); err != nil {
		t.Fatal(err)
	}
	cipher, err := secret.New([]byte("synthetic-list-key-32-bytes-long!!"))
	if err != nil {
		t.Fatal(err)
	}
	domain := &listPublicationDomain{candidates: []listcollections.Candidate{{ContentID: id, Kind: "movie", ExternalIDs: map[string]string{"tmdb": "42"}}}}
	service := &listcollections.Service{Pool: pool, Cipher: cipher, Domain: domain}
	subject := listcollections.Subject{UserID: user, ProfileID: "owner", InstallationID: install, InstanceID: "instance"}
	return service, subject, domain
}

func TestListFollowDurableSnapshotPublication(t *testing.T) {
	service, subject, domain := listPublicationFixture(t)
	pool, ctx := service.Pool, t.Context()
	user, install := subject.UserID, subject.InstallationID
	preview, err := service.QueuePreview(ctx, subject, listcollections.PreviewInput{URL: "https://pastime.example/lists/1"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := service.PollTx(ctx, tx, install, "instance", 3)
	if err != nil || len(jobs) != 1 {
		tx.Rollback(ctx)
		t.Fatal("preview job", len(jobs), err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = service.ResolvedTx(ctx, tx, install, listcollections.Resolution{JobID: preview.ID, LeaseToken: jobs[0].LeaseToken, Generation: jobs[0].Generation, SourceID: "personal:stable", Name: "Native source", Kind: "manual", Count: 2, ResolutionToken: "synthetic-grant", SigningKey: base64.RawURLEncoding.EncodeToString(pub), ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	binding, err := service.CreateBinding(ctx, subject, listcollections.CreateInput{PreviewID: preview.ID, Scope: "personal", Name: "My customization"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.CreateBinding(ctx, subject, listcollections.CreateInput{PreviewID: preview.ID, Scope: "personal", Name: "My customization"})
	if err != nil || again.ID != binding.ID {
		t.Fatal("creation replay duplicated target", err)
	}
	if _, err = service.CreateBinding(ctx, subject, listcollections.CreateInput{PreviewID: preview.ID, Scope: "personal", Name: "My customization", IsShared: true}); !errors.Is(err, listcollections.ErrConflict) {
		t.Fatal("changed audience silently reused creation receipt", err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err = service.PollTx(ctx, tx, install, "instance", 3)
	if err != nil || len(jobs) != 1 {
		tx.Rollback(ctx)
		t.Fatal("refresh job", len(jobs), err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	job := jobs[0]
	coalesced, err := service.Refresh(ctx, subject, binding.ID)
	if err != nil || coalesced.ID != job.ID {
		t.Fatal("manual refresh did not coalesce the active job", err)
	}
	entries := []listcollections.Entry{{Kind: "movie", ExternalIDs: map[string]string{"tmdb": "42"}}, {Kind: "movie", ExternalIDs: map[string]string{"tmdb": "99"}}}
	digest, err := listcollections.Digest(entries)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-10 * time.Second)
	page := listcollections.Page{Version: 1, JobID: job.ID, BindingID: binding.ID, Generation: job.Generation, LeaseToken: job.LeaseToken, SourceID: binding.SourceID, SnapshotID: uuid.NewString(), Total: 2, Digest: digest, GeneratedAt: now, ExpiresAt: now.Add(time.Minute), Final: true, Entries: entries}
	stage := func(p listcollections.Page) error {
		tx, e := pool.Begin(ctx)
		if e != nil {
			return e
		}
		defer tx.Rollback(ctx)
		if e = service.StageTx(ctx, tx, install, p); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	first := page
	first.Final = false
	first.NextCursor = "next"
	first.Entries = entries[:1]
	if err = stage(first); err != nil {
		t.Fatal(err)
	}
	if err = stage(first); err != nil {
		t.Fatal("identical page retry", err)
	}
	proof := listcollections.SourceProof{Version: 1, InstanceID: subject.InstanceID, AccountID: strconv.Itoa(user), ProfileID: subject.ProfileID, SourceID: page.SourceID, SnapshotID: page.SnapshotID, Digest: page.Digest, IssuedAt: time.Now(), ExpiresAt: page.ExpiresAt}
	raw, _ := json.Marshal(proof)
	signature := ed25519.Sign(key, append([]byte("bloem.list.proof.v1\x00"), raw...))
	completion := listcollections.Completion{JobID: job.ID, BindingID: binding.ID, Generation: job.Generation, LeaseToken: job.LeaseToken, SnapshotID: page.SnapshotID, Digest: page.Digest, SourceAttestation: "lp1." + base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(signature)}
	complete := func() (listcollections.Receipt, error) {
		tx, e := pool.Begin(ctx)
		if e != nil {
			return listcollections.Receipt{}, e
		}
		defer tx.Rollback(ctx)
		r, e := service.CompleteTx(ctx, tx, install, completion)
		if e != nil {
			return r, e
		}
		return r, tx.Commit(ctx)
	}
	if _, err = complete(); !errors.Is(err, listcollections.ErrSnapshot) {
		t.Fatal("missing final page published", err)
	}
	last := page
	last.Position = 1
	last.Entries = entries[1:]
	if err = stage(last); err != nil {
		t.Fatal(err)
	}
	validProof := completion.SourceAttestation
	completion.SourceAttestation += "x"
	if _, err = complete(); !errors.Is(err, listcollections.ErrSnapshot) {
		t.Fatal("forged source proof published", err)
	}
	completion.SourceAttestation = validProof
	old := proof
	old.IssuedAt = page.GeneratedAt.Add(time.Second)
	oldRaw, _ := json.Marshal(old)
	oldSignature := ed25519.Sign(key, append([]byte("bloem.list.proof.v1\x00"), oldRaw...))
	completion.SourceAttestation = "lp1." + base64.RawURLEncoding.EncodeToString(oldRaw) + "." + base64.RawURLEncoding.EncodeToString(oldSignature)
	if _, err = complete(); !errors.Is(err, listcollections.ErrSnapshot) {
		t.Fatal("source proof issued before the host claim published", err)
	}
	completion.SourceAttestation = validProof
	domain.fail = true
	if _, err = complete(); err == nil {
		t.Fatal("forced publication failure ignored")
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM user_personal_collection_items WHERE collection_id=$1`, binding.TargetID).Scan(&count); err != nil || count != 0 {
		t.Fatal("publication rollback leaked members", count, err)
	}
	domain.fail = false
	receipt, err := complete()
	if err != nil || receipt.TargetID != binding.TargetID || receipt.Revision == 0 {
		t.Fatal("publication failed", receipt, err)
	}
	second, err := complete()
	if err != nil || second != receipt {
		t.Fatal("completion replay changed native revision", err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM bloem_list_request_intents WHERE binding_id=$1 AND wanted`, binding.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("missing intent not committed atomically", count, err)
	}
	if _, err = service.Refresh(ctx, subject, binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = complete(); !errors.Is(err, listcollections.ErrConflict) {
		t.Fatal("older generation replayed after a newer refresh", err)
	}
	// A source can be disabled/revoked while local ownership remains valid.
	domain.deniedProfile = subject.ProfileID
	if err = service.Disconnect(ctx, subject, binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = complete(); err == nil {
		t.Fatal("detached generation published/replayed")
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM bloem_list_request_intents WHERE binding_id=$1 AND wanted`, binding.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("disconnect retained unsent work", count, err)
	}
}

func TestListFollowPollingHasInstallationConcurrencyBound(t *testing.T) {
	s, subject, _ := listPublicationFixture(t)
	for i := 0; i < 8; i++ {
		if _, err := s.QueuePreview(t.Context(), subject, listcollections.PreviewInput{URL: "https://pastime.example/lists/1"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.QueuePreview(t.Context(), subject, listcollections.PreviewInput{URL: "https://pastime.example/lists/1"}); !errors.Is(err, listcollections.ErrConflict) {
		t.Fatal("preview admission bound", err)
	}
	poll := func() ([]listcollections.Job, error) {
		tx, e := s.Pool.Begin(t.Context())
		if e != nil {
			return nil, e
		}
		defer tx.Rollback(t.Context())
		j, e := s.PollTx(t.Context(), tx, subject.InstallationID, subject.InstanceID, 100)
		if e != nil {
			return nil, e
		}
		return j, tx.Commit(t.Context())
	}
	jobs, err := poll()
	if err != nil || len(jobs) != 3 {
		t.Fatal("initial bounded claims", len(jobs), err)
	}
	jobs, err = poll()
	if err != nil || len(jobs) != 0 {
		t.Fatal("second replica exceeded three active installation claims", len(jobs), err)
	}
}

func TestListFollowRevokedQueuedProfileDoesNotBlockOtherJobs(t *testing.T) {
	s, subject, domain := listPublicationFixture(t)
	for _, profile := range []string{"revoked", "allowed"} {
		owner := subject
		owner.ProfileID = profile
		if _, err := s.QueuePreview(t.Context(), owner, listcollections.PreviewInput{URL: "https://pastime.example/lists/1"}); err != nil {
			t.Fatal(err)
		}
	}
	domain.deniedProfile = "revoked"
	tx, err := s.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	jobs, err := s.PollTx(t.Context(), tx, subject.InstallationID, subject.InstanceID, 3)
	if err != nil || len(jobs) != 1 || jobs[0].ProfileID != "allowed" {
		t.Fatal("revoked queued actor blocked healthy actor", len(jobs), err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_list_jobs WHERE state='failed' AND failure='denied'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("revoked work not terminated", count, err)
	}
}
