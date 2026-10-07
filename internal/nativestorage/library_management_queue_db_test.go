//go:build integration

package nativestorage_test

import (
	"errors"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanqueue"
	"github.com/Silo-Server/silo-server/internal/sections"
	"testing"
)

func TestNativeOnboardingLibraryActualQueueDB(t *testing.T) {
	pool, actor, source, id := nativestorage.NativeDomainLegalFixtureForTest(t)
	folders := catalog.NewFolderRepository(pool)
	repo := scanqueue.NewRepository(pool)
	// This is the actual existing service/repository, not a queue copy. This test
	// proves command admission/acceptance/coalescing; consumer execution is A's gate.
	queue := scanqueue.NewService(repo, folders, nil, nil, t.Context(), 1, 1)
	libs := nativestorage.NewLibraryManagement(pool, folders, sections.NewRepository(pool), resourcetenancy.NewStore(pool), queue)
	first, err := libs.Scan(t.Context(), actor, id, 3, source.ConfigurationRevision)
	if err != nil {
		t.Fatalf("actual structural queue Scan must succeed: %v", err)
	}
	if first.ScanRunID == "" || !first.Created || first.Mode != "library" || first.State != "accepted" {
		t.Fatal("real accepted queue result absent")
	}
	actual, err := repo.GetByID(t.Context(), first.ScanRunID)
	if err != nil || actual.ID != first.ScanRunID || actual.MediaFolderID != id || actual.Status != "accepted" {
		t.Fatal("durable real queue row absent")
	}
	second, err := libs.Scan(t.Context(), actor, id, 3, source.ConfigurationRevision)
	if err != nil || second.Created || second.ScanRunID != first.ScanRunID {
		t.Fatal("accepted run did not coalesce")
	}
	if _, err = repo.Start(t.Context(), first.ScanRunID); err != nil {
		t.Fatal(err)
	}
	running, err := libs.Scan(t.Context(), actor, id, 3, source.ConfigurationRevision)
	if err != nil || running.Created || running.ScanRunID != first.ScanRunID || running.State != "running" {
		t.Fatal("running run did not coalesce")
	}
	if _, err = repo.Complete(t.Context(), first.ScanRunID, nil); err != nil {
		t.Fatal(err)
	}
	active, err := repo.GetActiveByScope(t.Context(), id, "library", "")
	if err != nil || active.ID == first.ScanRunID {
		t.Fatal("actual owed follow-up absent")
	}
	coalesced, err := libs.Scan(t.Context(), actor, id, 3, source.ConfigurationRevision)
	if err != nil || coalesced.Created || coalesced.ScanRunID != active.ID {
		t.Fatal("follow-up was not reused")
	}
	if _, err = repo.Start(t.Context(), active.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Complete(t.Context(), active.ID, nil); err != nil {
		t.Fatal(err)
	}
	newer, err := libs.Scan(t.Context(), actor, id, 3, source.ConfigurationRevision)
	if err != nil || !newer.Created || newer.ScanRunID == first.ScanRunID {
		t.Fatal("completed scope did not create a new run")
	}
	var before int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM scan_runs WHERE media_folder_id=$1", id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = auth.NewSessionRepository(pool).Revoke(t.Context(), actor.SessionID); err != nil {
		t.Fatal(err)
	}
	_, err = libs.Scan(t.Context(), actor, id, 3, source.ConfigurationRevision)
	var typed *catalog.NativeOnboardingError
	if !errors.As(err, &typed) || typed.Code != "authorization_state_stale" {
		t.Fatal("revoked login reached queue")
	}
	var after int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM scan_runs WHERE media_folder_id=$1", id).Scan(&after); err != nil || after != before {
		t.Fatal("revoked queue attempt added durable work")
	}
}
