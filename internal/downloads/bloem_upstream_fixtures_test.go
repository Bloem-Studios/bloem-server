package downloads

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They are copied unchanged so the Silo files stay deleted.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func (f managedFixture) createManagedEntry(t *testing.T) string {
	t.Helper()
	id := fmt.Sprintf("dl-%d", time.Now().UnixNano())
	now := time.Now()
	if err := f.repo.Create(context.Background(), &Download{
		ID: id, UserID: f.userID, ProfileID: f.profileA, DeviceID: f.deviceA,
		MediaFileID: f.fileID, ContentID: f.contentID, Kind: KindQueued,
		Status: StatusReady, Format: FormatOriginal, FileSize: 1024,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create managed entry: %v", err)
	}
	return id
}

func artifactStatus(t *testing.T, repo *ArtifactRepository, id string) string {
	t.Helper()
	a, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a.Status
}
