//go:build integration

package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

func TestListFollowCleanupRestoresOrphanTargets(t *testing.T) {
	s, subject, _ := listPublicationFixture(t)
	ctx := t.Context()
	store, err := pgstore.NewPostgresProvider(s.Pool).ForUser(ctx, subject.UserID)
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]string{"bloem_list_binding": uuid.NewString()})
	linked, err := store.CreateCollection(ctx, userstore.CreateCollectionInput{CreatorProfileID: subject.ProfileID, Name: "Orphan", CollectionType: "synced", SourceConfig: string(config)})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AddCollectionItem(ctx, linked.ID, "synthetic-preserved", 0); err != nil {
		t.Fatal(err)
	}
	ordinary, err := store.CreateCollection(ctx, userstore.CreateCollectionInput{CreatorProfileID: subject.ProfileID, Name: "Other source", CollectionType: "synced", SourceConfig: `{"url":"https://example.invalid/list"}`})
	if err != nil {
		t.Fatal(err)
	}
	var folder int
	if err = s.Pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies','Synthetic cleanup library',true) RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	server, err := catalog.NewLibraryCollectionRepository(s.Pool).Create(ctx, catalog.CreateLibraryCollectionInput{LibraryID: folder, Title: "Orphan server", Slug: uuid.NewString(), SourceConfig: config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `DELETE FROM bloem_managed_grants WHERE installation_id=$1`, subject.InstallationID); err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupTargets(ctx, 100); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetCollection(ctx, linked.ID)
	if err != nil || got.CollectionType != "manual" {
		t.Fatalf("orphan remains linked: %v %v", got, err)
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM user_personal_collection_items WHERE collection_id=$1 AND media_item_id='synthetic-preserved'`, linked.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("orphan membership lost: %d %v", count, err)
	}
	got, err = store.GetCollection(ctx, ordinary.ID)
	if err != nil || got.CollectionType != "synced" {
		t.Fatal("ordinary source changed", got, err)
	}
	var source string
	if err = s.Pool.QueryRow(ctx, `SELECT COALESCE(source_config->>'bloem_list_binding','') FROM library_collections WHERE id=$1`, server.ID).Scan(&source); err != nil || source != "" {
		t.Fatal("orphan server marker retained", source, err)
	}
	if err = s.CleanupTargets(ctx, 100); err != nil {
		t.Fatal("cleanup replay", err)
	}
}

func TestListFollowCleanupTombstonesDeletedTargetAndCancelsOnlyUnsentRequests(t *testing.T) {
	s, subject, _ := listPublicationFixture(t)
	ctx := t.Context()
	binding, job := uuid.NewString(), uuid.NewString()
	raw, _ := json.Marshal(subject)
	if _, err := s.Pool.Exec(ctx, `INSERT INTO bloem_list_bindings(id,installation_id,user_id,profile_id,subject,preview_id,create_digest,source_id,signing_key,grant_cipher,scope,target_id) VALUES($1,$2,$3,$4,$5,$6,$7,'source',$8,'synthetic','personal','deleted-target')`, binding, subject.InstallationID, subject.UserID, subject.ProfileID, raw, uuid.NewString(), make([]byte, 32), strings.Repeat("A", 43)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO bloem_list_jobs(id,installation_id,subject,mode,binding_id,generation) VALUES($1,$2,$3,'refresh',$4,1)`, job, subject.InstallationID, raw, binding); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"pending", "delivered"} {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO bloem_list_request_intents(binding_id,identity_key,entry,generation,status) VALUES($1,$2,'{}',1,$2)`, binding, status); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CleanupTargets(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var status string
	var generation int
	if err := s.Pool.QueryRow(ctx, `SELECT status,generation FROM bloem_list_bindings WHERE id=$1`, binding).Scan(&status, &generation); err != nil || status != "detached" || generation != 2 {
		t.Fatal("deleted target not fenced", status, generation, err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT state FROM bloem_list_jobs WHERE id=$1`, job).Scan(&status); err != nil || status != "failed" {
		t.Fatal("deleted target retains active job", status, err)
	}
	var wanted bool
	for _, key := range []string{"pending", "delivered"} {
		if err := s.Pool.QueryRow(ctx, `SELECT status,wanted FROM bloem_list_request_intents WHERE binding_id=$1 AND identity_key=$2`, binding, key).Scan(&status, &wanted); err != nil {
			t.Fatal(err)
		}
		want := key
		if key == "pending" {
			want = "cancelled"
		}
		if wanted || status != want {
			t.Fatal("cleanup altered issued request or retained unsent work", status, wanted)
		}
	}
}
