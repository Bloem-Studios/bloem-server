package downloads

// Bloem's copies of Silo's replacement-race tests (service_quota_test.go,
// Silo #4202). Silo's helper sees the request wait by its polling query,
// pg_try_advisory_xact_lock; Bloem's quota lock waits in pg_advisory_lock
// instead (withBloemDownloadQuotaLock), so these copies look for the
// ungranted lock in pg_locks. The fixture and outcomes are Silo's.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// bloemReplaceWhileTheLockIsHeld registers episode 1 of a fixture on device A with
// status, then runs a season request that replaces it with a 5 Mbps version
// still to prepare, at a concurrent limit of 1. The test holds the account's
// quota lock until the request has read the entry and is trying for the lock,
// runs change, then lets the request go and returns its error.
func bloemReplaceWhileTheLockIsHeld(t *testing.T, status string, change func(ctx context.Context, f managedFixture, existingID, ep2 string, ep2File int)) (managedFixture, string, int, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	f := seedManagedFixture(t)
	ep1, ep2 := f.contentID+"-ep-1", f.contentID+"-ep-2"
	ep1File, ep2File := f.episodeFile(t, ep1), f.episodeFile(t, ep2)
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM download_artifacts WHERE media_file_id = ANY($1)`, []int{ep1File, ep2File})
	})
	existingID := fmt.Sprintf("dl-ep-%d", time.Now().UnixNano())
	now := time.Now()
	if err := f.repo.Create(ctx, &Download{
		ID: existingID, UserID: f.userID, ProfileID: f.profileA, DeviceID: f.deviceA, MediaFileID: ep1File,
		ContentID: f.contentID, EpisodeID: ep1, Kind: KindQueued, Status: status,
		Format: FormatOriginal, Quality: QualityOriginal, EffectiveQuality: QualityOriginal,
		FileSize: 1024, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// The request runs on its own pool, named so the test can see it poll
	// for the lock.
	appName := "dl-replace-race-" + existingID
	cfg := f.pool.Config()
	cfg.ConnConfig.RuntimeParams["application_name"] = appName
	reqPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reqPool.Close)
	reqRepo := NewRepository(reqPool)
	svc := NewService(reqRepo, nil, NewQuantityLimiter(reqRepo, 1, 0, 0), nil, nil, nil, nil, nil, nil, &config.DownloadConfig{Enabled: true})
	svc.SetArtifactManager(NewArtifactManager(
		NewArtifactRepository(reqPool), reqRepo, nil, stubQuotaPreparer{}, "replacement-race-test",
		func() *config.Config { return nil }, nil,
	))
	decision := QualityDecision{
		RequestedQuality: Quality5Mbps, EffectiveQuality: Quality5Mbps, DeliveryFormat: FormatTranscode,
		TargetBitrateKbps: 5000, RequiresArtifact: true,
		PrepareTarget: playback.PrepareTarget{Container: "mp4", CodecVideo: "h264", CodecAudio: "aac", TargetBitrateKbps: 5000},
	}
	req := CreateRequest{ContentID: f.contentID, ProfileID: f.profileA, DeviceID: f.deviceA,
		ExpectedEntries: map[string]ManagedCreateExpectation{ep1: {ID: existingID, Revision: 1}}}
	item := managedItem{file: &models.MediaFile{ID: ep1File, ContentID: f.contentID, EpisodeID: ep1, FileSize: 1024}, contentID: f.contentID, episodeID: ep1}

	lock, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, downloadQuotaLockClassID, f.userID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := svc.ensureManagedDecisions(ctx, f.userID, req, []managedItem{item}, []QualityDecision{decision}, "season")
		done <- err
	}()
	// The request has read the entry once it tries for the lock.
	for {
		var polling bool
		// Bloem's quota lock waits in pg_advisory_lock on its own
		// connection (withBloemDownloadQuotaLock), so the request shows as an
		// ungranted advisory lock on the account's quota key.
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
			WHERE a.application_name = $1 AND l.locktype = 'advisory' AND NOT l.granted
			  AND l.classid = $2::oid AND l.objid = $3::oid AND l.objsubid = 2)`, appName, downloadQuotaLockClassID, f.userID).Scan(&polling); err != nil {
			t.Fatal(err)
		}
		if polling {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("request finished without waiting for the lock: %v", err)
		case <-ctx.Done():
			t.Fatal("request never tried for the quota lock")
		case <-time.After(5 * time.Millisecond):
		}
	}
	change(ctx, f, existingID, ep2, ep2File)
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	return f, existingID, ep1File, <-done
}

// TestBloemPreparedReplacementCountsTheEntryStatusUnderTheLockDB pins that a season
// replacement's concurrent slot does not depend on the entry's status when
// the request read it. The entry is preparing then; while the request waits
// for the lock, its conversion finishes (which leaves the revision alone) and
// another download takes the freed slot. The replacement would start a second
// conversion at a limit of 1, so it is refused.
func TestBloemPreparedReplacementCountsTheEntryStatusUnderTheLockDB(t *testing.T) {
	f, existingID, _, err := bloemReplaceWhileTheLockIsHeld(t, StatusPreparing, func(ctx context.Context, f managedFixture, existingID, ep2 string, ep2File int) {
		if _, err := f.pool.Exec(ctx, `UPDATE downloads SET status = $2 WHERE id = $1`, existingID, StatusReady); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if err := f.repo.Create(ctx, &Download{
			ID: existingID + "-other", UserID: f.userID, ProfileID: f.profileA, DeviceID: f.deviceA, MediaFileID: ep2File,
			ContentID: f.contentID, EpisodeID: ep2, Kind: KindQueued, Status: StatusPreparing,
			Format: FormatOriginal, Quality: QualityOriginal, EffectiveQuality: QualityOriginal,
			FileSize: 1024, Revision: 1, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	})
	if !errors.Is(err, ErrConcurrentLimitReached) {
		var preparing int
		_ = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM downloads WHERE user_id = $1 AND status = 'preparing'`, f.userID).Scan(&preparing)
		t.Fatalf("replacement after its entry's conversion finished: err = %v, want ErrConcurrentLimitReached (%d preparing at a limit of 1)", err, preparing)
	}
	row, err := f.repo.GetByID(context.Background(), existingID)
	if err != nil || row.Status != StatusReady || row.Revision != 1 {
		t.Fatalf("refused replacement changed the entry: %+v %v", row, err)
	}
}

// TestBloemPreparedReplacementRefusesAStaleGuardUnderTheLockDB pins that a season
// replacement whose entry another request replaced while it waited for the
// lock is refused before it queues an encode.
func TestBloemPreparedReplacementRefusesAStaleGuardUnderTheLockDB(t *testing.T) {
	f, _, ep1File, err := bloemReplaceWhileTheLockIsHeld(t, StatusReady, func(ctx context.Context, f managedFixture, existingID, _ string, _ int) {
		// The other request's replacement: a new revision, now preparing.
		if _, err := f.pool.Exec(ctx, `UPDATE downloads SET status = $2, revision = 2 WHERE id = $1`, existingID, StatusPreparing); err != nil {
			t.Fatal(err)
		}
	})
	if !errors.Is(err, ErrStatusConflict) {
		t.Fatalf("stale guard: err = %v, want ErrStatusConflict", err)
	}
	var jobs int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM download_artifacts WHERE media_file_id = $1`, ep1File).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("stale guard queued %d encode jobs (%v)", jobs, err)
	}
}
