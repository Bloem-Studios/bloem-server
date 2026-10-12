//go:build integration

package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func nativeAudioFixture(t *testing.T) (*pgxpool.Pool, *Scanner, *models.MediaFolder, storagesource.Location, *storagesource.Repository) {
	t.Helper()
	pool, s, folder, location := mixedStorageFixture(t)
	folder.Type = "audiobooks"
	s.ffprobePath = "ffprobe"
	if _, err := pool.Exec(t.Context(), `UPDATE media_folders SET type='audiobooks' WHERE id=$1`, folder.ID); err != nil {
		t.Fatal(err)
	}
	return pool, s, folder, location, storagesource.NewRepository(pool)
}

func audioBegin(t *testing.T, r *storagesource.Repository, l storagesource.Location) storagesource.Lease {
	t.Helper()
	lease, err := r.Begin(t.Context(), l.SourceKey, "audio-publication", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func audioApply(t *testing.T, r *storagesource.Repository, s *Scanner, folder *models.MediaFolder, l storagesource.Location, lease storagesource.Lease, complete bool, cursor string, tracks ...StorageAudioTrack) StorageAudioResult {
	t.Helper()
	return audioApplyWithRemoved(t, r, s, folder, l, lease, complete, cursor, nil, tracks...)
}

func audioApplyWithRemoved(t *testing.T, r *storagesource.Repository, s *Scanner, folder *models.MediaFolder, l storagesource.Location, lease storagesource.Lease, complete bool, cursor string, removed []string, tracks ...StorageAudioTrack) StorageAudioResult {
	t.Helper()
	cp, more, err := r.NextDirectory(t.Context(), lease)
	if err != nil || !more {
		t.Fatalf("checkpoint=%+v more=%v err=%v", cp, more, err)
	}
	entries := make([]*storagev1.Entry, len(tracks))
	for i, track := range tracks {
		entries[i] = track.Entry
	}
	page := &storagev1.ListResponse{Entries: entries, Complete: complete, NextCursor: cursor, RemovedEntryIds: removed}
	var result StorageAudioResult
	err = r.ApplyPage(t.Context(), lease, cp, page, func(ctx context.Context, tx pgx.Tx, files []*storagev1.Entry, _ []string) error {
		allowed := map[string]bool{}
		for _, e := range files {
			allowed[e.Id] = true
		}
		var selected []StorageAudioTrack
		for _, track := range tracks {
			if allowed[track.Entry.Id] {
				selected = append(selected, track)
			}
		}
		var e error
		result, e = s.PublishStorageAudioPageTx(ctx, tx, StorageAudioPage{Folder: folder, Location: l, Lease: lease, ConfigurationRevision: lease.ConfigurationRevision, DirectoryID: cp.DirectoryID, Complete: complete, Tracks: selected})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func audioComplete(t *testing.T, r *storagesource.Repository, s *Scanner, f *models.MediaFolder, l storagesource.Location, lease storagesource.Lease) StorageAudioResult {
	t.Helper()
	var result StorageAudioResult
	_, err := r.CompleteLocationAuthorized(t.Context(), lease, func(context.Context, pgx.Tx) error { return nil }, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		result, _, e = s.FinalizeStorageAudioLocationTx(ctx, tx, f, l, lease)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNativeAudioPublishesOrderedPresentationDB(t *testing.T) {
	pool, s, f, l, r := nativeAudioFixture(t)
	lease := audioBegin(t, r, l)
	tracks := []StorageAudioTrack{audioTestTrack("d2t2", 2, 2), audioTestTrack("d1t2", 1, 2), audioTestTrack("d2t1", 2, 1), audioTestTrack("d1t1", 1, 1)}
	tracks[3].Entry.Audiobook = &storagev1.AudiobookMetadata{Title: "Fixture Book", Authors: []string{"Fixture Author"}, Narrators: []string{"Fixture Narrator"}}
	result := audioApply(t, r, s, f, l, lease, true, "", tracks...)
	if result.New != 4 || result.BooksPublished != 1 {
		t.Fatalf("result=%+v", result)
	}
	key, _ := storagesource.AudioPresentationKey(l.ID, "root")
	rows, err := pool.Query(t.Context(), `SELECT f.presentation_part_index,f.presentation_part_total,f.duration,r.entry_id,count(m.root_id) FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id LEFT JOIN bloem_storage_file_roots m ON m.media_file_id=f.id AND m.enabled WHERE f.media_folder_id=$1 AND f.presentation_group_key=$2 GROUP BY f.id,r.entry_id ORDER BY f.presentation_part_index`, f.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	part := 0
	for rows.Next() {
		part++
		var index, total, duration, roots int
		var entry string
		if err := rows.Scan(&index, &total, &duration, &entry, &roots); err != nil {
			t.Fatal(err)
		}
		want := []string{"d1t1", "d1t2", "d2t1", "d2t2"}[part-1]
		if index != part || total != 4 || duration != 2 || entry != want || roots == 0 {
			t.Fatalf("part=%d total=%d duration=%d entry=%s roots=%d", index, total, duration, entry, roots)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if part != 4 {
		t.Fatalf("parts=%d", part)
	}
	audioComplete(t, r, s, f, l, lease)
	var staged int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_audio_staged_tracks WHERE location_id=$1`, l.ID).Scan(&staged); err != nil || staged != 0 {
		t.Fatalf("staging=%d err=%v", staged, err)
	}
	for _, track := range tracks {
		var chapters []byte
		if err := pool.QueryRow(t.Context(), `SELECT f.chapters FROM media_files f JOIN bloem_storage_file_refs x ON x.media_file_id=f.id WHERE x.location_id=$1 AND x.entry_id=$2`, l.ID, track.Entry.Id).Scan(&chapters); err != nil {
			t.Fatal(err)
		}
		var decoded []models.MediaChapter
		if err := json.Unmarshal(chapters, &decoded); err != nil || len(decoded) != 1 || decoded[0].StartSeconds != 0 || decoded[0].EndSeconds != 2.75 {
			t.Fatalf("chapters=%s err=%v", chapters, err)
		}
	}
}

func TestNativeAudioInitialMetadataPreservesEnrichmentDuringLockWaitDB(t *testing.T) {
	pool, s, folder, location, journal := nativeAudioFixture(t)
	track := audioTestTrack("1", 1, 1)
	track.Probe.Tags["album"] = "Initial tagged title"
	lease := audioBegin(t, journal, location)
	audioApply(t, journal, s, folder, location, lease, true, "", track)
	audioComplete(t, journal, s, folder, location, lease)
	var contentID string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1`, folder.ID).Scan(&contentID); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	enrichment, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enrichment.Rollback(context.Background()) }()
	title, overview := "Enriched title", "Enriched description"
	if err := s.itemRepo.UpdateMetadataTx(ctx, enrichment, contentID, &catalog.MetadataUpdate{Title: &title, Overview: &overview}); err != nil {
		t.Fatal(err)
	}
	if _, err := enrichment.Exec(ctx, `UPDATE media_items SET status='matched',last_refreshed=clock_timestamp(),matched_at=clock_timestamp() WHERE content_id=$1`, contentID); err != nil {
		t.Fatal(err)
	}
	var enrichmentPID int
	if err := enrichment.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&enrichmentPID); err != nil {
		t.Fatal(err)
	}
	lease = audioBegin(t, journal, location)
	cp, more, err := journal.NextDirectory(ctx, lease)
	if err != nil || !more {
		t.Fatalf("checkpoint=%+v more=%v err=%v", cp, more, err)
	}
	done := make(chan error, 1)
	go func() {
		done <- journal.ApplyPage(ctx, lease, cp, &storagev1.ListResponse{Entries: []*storagev1.Entry{track.Entry}, Complete: true}, func(ctx context.Context, tx pgx.Tx, _ []*storagev1.Entry, _ []string) error {
			_, err := s.PublishStorageAudioPageTx(ctx, tx, StorageAudioPage{Folder: folder, Location: location, Lease: lease, ConfigurationRevision: lease.ConfigurationRevision, DirectoryID: cp.DirectoryID, Complete: true, Tracks: []StorageAudioTrack{track}})
			return err
		})
	}()
	// Observe the real row-lock wait before enrichment commits. At this point
	// the publisher has read the old pending item under READ COMMITTED.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%media_items%FOR UPDATE%')`, enrichmentPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("publisher completed before lock wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err := enrichment.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var gotTitle, gotOverview, status string
	if err := pool.QueryRow(ctx, `SELECT title,overview,status FROM media_items WHERE content_id=$1`, contentID).Scan(&gotTitle, &gotOverview, &status); err != nil {
		t.Fatal(err)
	}
	if gotTitle != title || gotOverview != overview || status != "matched" {
		t.Fatalf("enrichment overwritten: title=%q overview=%q status=%q", gotTitle, gotOverview, status)
	}
	audioComplete(t, journal, s, folder, location, lease)
}

func TestNativeAudioCompletesBookOnEmptyFinalPageDB(t *testing.T) {
	pool, s, f, l, r := nativeAudioFixture(t)
	lease := audioBegin(t, r, l)
	if result := audioApply(t, r, s, f, l, lease, false, "next", audioTestTrack("1", 1, 1)); result.New != 0 {
		t.Fatalf("early=%+v", result)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files WHERE media_folder_id=$1`, f.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("early visible=%d err=%v", count, err)
	}
	if result := audioApply(t, r, s, f, l, lease, true, ""); result.New != 1 {
		t.Fatalf("empty final=%+v", result)
	}
	audioComplete(t, r, s, f, l, lease)
}

func TestNativeAudioFirstOmissionKeepsPriorPresentationDB(t *testing.T) {
	pool, s, f, l, r := nativeAudioFixture(t)
	tracks := []StorageAudioTrack{audioTestTrack("1", 1, 1), audioTestTrack("2", 1, 2), audioTestTrack("3", 1, 3)}
	lease := audioBegin(t, r, l)
	audioApply(t, r, s, f, l, lease, true, "", tracks...)
	audioComplete(t, r, s, f, l, lease)
	for run := 1; run <= 2; run++ {
		lease = audioBegin(t, r, l)
		audioApply(t, r, s, f, l, lease, true, "", tracks[:2]...)
		audioComplete(t, r, s, f, l, lease)
		var present, total int
		err := pool.QueryRow(t.Context(), `SELECT count(*),COALESCE(max(presentation_part_total),0) FROM media_files WHERE media_folder_id=$1 AND missing_since IS NULL`, f.ID).Scan(&present, &total)
		want := 3
		if run == 2 {
			want = 2
		}
		if err != nil || present != want || total != want {
			t.Fatalf("run%d present%d total%d want%d err%v", run, present, total, want, err)
		}
	}
}

func TestNativeAudioStagesIncompleteBookAcrossPagesDB(t *testing.T) {
	pool, s, f, l, r := nativeAudioFixture(t)
	lease := audioBegin(t, r, l)
	for i, id := range []string{"1", "2"} {
		audioApply(t, r, s, f, l, lease, false, id, audioTestTrack(id, 1, uint32(i+1)))
		var count int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files WHERE media_folder_id=$1`, f.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial visibility=%d err=%v", count, err)
		}
	}
	fresh := NewScanner(NewFileRepository(pool), "", nil, 1, false, 0)
	fresh.ffprobePath = "ffprobe"
	takeover := audioBegin(t, r, l)
	if takeover.RunID != lease.RunID || takeover.Epoch <= lease.Epoch {
		t.Fatalf("takeover=%+v original=%+v", takeover, lease)
	}
	result := audioApply(t, r, fresh, f, l, takeover, true, "", audioTestTrack("3", 1, 3))
	if result.New != 3 || result.BooksPublished != 1 {
		t.Fatalf("result=%+v", result)
	}
	audioComplete(t, r, fresh, f, l, takeover)
}

func TestNativeAudioSingleTrackKeepsSiloSingleFileSemanticsDB(t *testing.T) {
	pool, s, f, l, r := nativeAudioFixture(t)
	track := audioTestTrack("1", 1, 1)
	track.Probe.Chapters = []ChapterInfo{{Index: 0, Title: "Embedded", StartSeconds: 0, EndSeconds: 2.75, Source: "embedded"}}
	lease := audioBegin(t, r, l)
	audioApply(t, r, s, f, l, lease, true, "", track)
	var kind, key, canonical, observed string
	var index, total int
	var chapters []byte
	if err := pool.QueryRow(t.Context(), `SELECT presentation_kind,presentation_group_key,COALESCE(presentation_part_index,0),COALESCE(presentation_part_total,0),chapters,canonical_root_path,observed_root_path FROM media_files WHERE media_folder_id=$1`, f.ID).Scan(&kind, &key, &index, &total, &chapters, &canonical, &observed); err != nil {
		t.Fatal(err)
	}
	if kind != "" || index != 0 || total != 0 || !storagesource.IsNativeAudioPresentationKey(key) || canonical != "" || observed != "" {
		t.Fatalf("single kind=%s key=%s index=%d total=%d roots=%s/%s", kind, key, index, total, canonical, observed)
	}
	var got []models.MediaChapter
	if err := json.Unmarshal(chapters, &got); err != nil || len(got) != 1 || got[0].EndSeconds != 2.75 || got[0].Source != "embedded" {
		t.Fatalf("chapters=%s err=%v", chapters, err)
	}
	audioComplete(t, r, s, f, l, lease)
}

func TestNativeAudioTruncatedDurationKeepsExactTerminalChapterDB(t *testing.T) {
	pool, s, f, l, r := nativeAudioFixture(t)
	track := audioTestTrack("1", 1, 1)
	track.Probe.Chapters = []ChapterInfo{{Index: 0, Title: "Exact", StartSeconds: 0, EndSeconds: 2.75, Source: "embedded"}}
	lease := audioBegin(t, r, l)
	result := audioApply(t, r, s, f, l, lease, true, "", track)
	var duration int
	var end float64
	if err := pool.QueryRow(t.Context(), `SELECT duration,(chapters->0->>'end_seconds')::float8 FROM media_files WHERE media_folder_id=$1`, f.ID).Scan(&duration, &end); err != nil {
		t.Fatal(err)
	}
	if result.BooksPublished != 1 || duration != 2 || end != 2.75 {
		t.Fatalf("duration=%d end=%f result=%+v", duration, end, result)
	}
	audioComplete(t, r, s, f, l, lease)
}

func TestNativeAudioRejectedBookKeepsPublishedPresentationDB(t *testing.T) {
	for _, reason := range []string{"invalid", "unsupported", "subsecond", "negative chapter", "beyond duration", "nan chapter", "duplicate positions"} {
		t.Run(reason, func(t *testing.T) {
			pool, s, f, l, r := nativeAudioFixture(t)
			tracks := []StorageAudioTrack{audioTestTrack("1", 1, 1), audioTestTrack("2", 1, 2)}
			lease := audioBegin(t, r, l)
			audioApply(t, r, s, f, l, lease, true, "", tracks...)
			audioComplete(t, r, s, f, l, lease)
			snapshot := func() string {
				var text string
				if err := pool.QueryRow(t.Context(), `SELECT jsonb_agg(to_jsonb(f) ORDER BY id)::text FROM media_files f WHERE media_folder_id=$1`, f.ID).Scan(&text); err != nil {
					t.Fatal(err)
				}
				return text
			}
			before := snapshot()
			extra := audioTestTrack("3", 1, 3)
			switch reason {
			case "invalid":
				extra.Status = StorageAudioTrackInvalid
				extra.Probe = nil
			case "unsupported":
				extra.Status = StorageAudioTrackUnsupported
				extra.Probe = nil
			case "subsecond":
				extra.Probe.DurationSeconds = .5
			case "negative chapter":
				extra.Probe.Chapters = []ChapterInfo{{StartSeconds: -1, EndSeconds: 1}}
			case "beyond duration":
				extra.Probe.Chapters = []ChapterInfo{{StartSeconds: 0, EndSeconds: 3}}
			case "nan chapter":
				extra.Probe.Chapters = []ChapterInfo{{StartSeconds: math.NaN(), EndSeconds: 1}}
			case "duplicate positions":
				extra.Entry.AudioFile.TrackNumber = 1
			}
			lease = audioBegin(t, r, l)
			result := audioApply(t, r, s, f, l, lease, true, "", append(tracks, extra)...)
			audioComplete(t, r, s, f, l, lease)
			if result.BooksRejected != 1 || snapshot() != before {
				t.Fatalf("rejection changed published files: %+v", result)
			}
		})
	}
	t.Run("replacement revision hides whole group", func(t *testing.T) {
		pool, s, f, l, r := nativeAudioFixture(t)
		tracks := []StorageAudioTrack{audioTestTrack("1", 1, 1), audioTestTrack("2", 1, 2)}
		lease := audioBegin(t, r, l)
		audioApply(t, r, s, f, l, lease, true, "", tracks...)
		audioComplete(t, r, s, f, l, lease)
		tracks[0].Entry.Revision = "r2"
		tracks[0].Status = StorageAudioTrackInvalid
		tracks[0].Probe = nil
		lease = audioBegin(t, r, l)
		result := audioApply(t, r, s, f, l, lease, true, "", tracks...)
		audioComplete(t, r, s, f, l, lease)
		var present int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files WHERE media_folder_id=$1 AND missing_since IS NULL`, f.ID).Scan(&present); err != nil || present != 0 || result.BooksRejected != 1 {
			t.Fatalf("present=%d result=%+v err=%v", present, result, err)
		}
	})
}

func TestNativeAudioStagingReplayAtTrackLimitDB(t *testing.T) {
	pool, s, f, l, r := nativeAudioFixture(t)
	lease := audioBegin(t, r, l)
	tracks := make([]StorageAudioTrack, 512)
	for i := range tracks {
		tracks[i] = audioTestTrack(fmt.Sprintf("%03d", i), 1, uint32(i+1))
	}
	audioApply(t, r, s, f, l, lease, false, "next", tracks...)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	result, err := s.PublishStorageAudioPageTx(t.Context(), tx, StorageAudioPage{Folder: f, Location: l, Lease: lease, ConfigurationRevision: lease.ConfigurationRevision, DirectoryID: "root", Complete: true, Tracks: tracks})
	if err != nil || result.BooksPublished != 1 || result.New != 512 {
		t.Fatalf("replay result=%+v err=%v", result, err)
	}
}

func audioItemSnapshot(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var snapshot string
	if err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object('item',to_jsonb(m),'credits',(SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM item_people p WHERE p.content_id=m.content_id),'series',(SELECT jsonb_agg(to_jsonb(s) ORDER BY s.content_id) FROM audiobook_series s WHERE s.content_id=m.content_id))::text FROM media_items m WHERE m.content_id=$1`, id).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestNativeAudioJoinsLocalItemWithoutOverwritingCuratedMetadataDB(t *testing.T) {
	pool, s, f, l, r := nativeAudioFixture(t)
	original := audioTestTrack("local", 1, 1)
	original.Entry.Audiobook = &storagev1.AudiobookMetadata{Title: "Curated book", Authors: []string{"Curated author"}, Narrators: []string{"Curated narrator"}, Isbn: fmt.Sprintf("curated-%d", f.ID), Series: "Curated series", SeriesIndex: "2"}
	lease := audioBegin(t, r, l)
	audioApply(t, r, s, f, l, lease, true, "", original, audioTestTrack("local2", 1, 2))
	audioComplete(t, r, s, f, l, lease)
	var id string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1 LIMIT 1`, f.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM bloem_storage_file_refs WHERE location_id=$1`, l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET file_path=$2||'/'||id||'.mp3',presentation_group_key='local-fixture' WHERE content_id=$1`, id, fmt.Sprintf("/fixture/%d/local.mp3", f.ID)); err != nil {
		t.Fatal(err)
	}
	before := audioItemSnapshot(t, pool, id)
	incoming := audioTestTrack("native", 1, 1)
	incoming.Entry.Audiobook = &storagev1.AudiobookMetadata{Title: "Incoming title", Authors: []string{"Incoming author"}, Isbn: original.Entry.Audiobook.Isbn, Series: "Incoming series"}
	lease = audioBegin(t, r, l)
	audioApply(t, r, s, f, l, lease, true, "", incoming, audioTestTrack("native2", 1, 2))
	audioComplete(t, r, s, f, l, lease)
	if after := audioItemSnapshot(t, pool, id); after != before {
		t.Fatalf("curated metadata changed\nbefore=%s\nafter=%s", before, after)
	}
	var items, groups int
	if err := pool.QueryRow(t.Context(), `SELECT count(DISTINCT content_id),count(DISTINCT presentation_group_key) FROM media_files WHERE media_folder_id=$1 AND missing_since IS NULL`, f.ID).Scan(&items, &groups); err != nil || items != 1 || groups != 2 {
		t.Fatalf("items=%d groups=%d err=%v", items, groups, err)
	}
	detailService := catalog.NewDetailService(catalog.NewItemRepository(pool), catalog.NewEpisodeRepository(pool), catalog.NewSeasonRepository(pool), catalog.NewPersonRepository(pool), s.fileRepo)
	detail, err := detailService.GetItemDetail(t.Context(), id, catalog.AccessFilter{})
	if err != nil || len(detail.PlaybackVariants) != 2 {
		t.Fatalf("detail variants=%+v err=%v", detail, err)
	}
	for _, variant := range detail.PlaybackVariants {
		if len(variant.Parts) != 2 {
			t.Fatalf("multipart variant=%+v", variant)
		}
	}

}

func TestNativeAudioForeignItemMetadataNotOverwrittenDB(t *testing.T) {
	pool, s, f, l, r := nativeAudioFixture(t)
	_, otherS, otherF, otherL, otherR := nativeAudioFixture(t)
	track := audioTestTrack("original", 1, 1)
	track.Entry.Audiobook = &storagev1.AudiobookMetadata{Title: "Original library title", Authors: []string{"Original author"}, Narrators: []string{"Original narrator"}, Isbn: fmt.Sprintf("foreign-%d", f.ID), Series: "Original series", SeriesIndex: "3"}
	lease := audioBegin(t, r, l)
	audioApply(t, r, s, f, l, lease, true, "", track)
	audioComplete(t, r, s, f, l, lease)
	var id string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1`, f.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	incoming := audioTestTrack("joining", 1, 1)
	incoming.Entry.Audiobook = &storagev1.AudiobookMetadata{Title: "Joining library title", Authors: []string{"Joining author"}, Isbn: track.Entry.Audiobook.Isbn, Series: "Joining series"}
	before := audioItemSnapshot(t, pool, id)
	for i := 0; i < 2; i++ {
		otherLease := audioBegin(t, otherR, otherL)
		audioApply(t, otherR, otherS, otherF, otherL, otherLease, true, "", incoming)
		audioComplete(t, otherR, otherS, otherF, otherL, otherLease)
		if after := audioItemSnapshot(t, pool, id); after != before {
			t.Fatal("joining library overwrote metadata")
		}
		lease = audioBegin(t, r, l)
		audioApply(t, r, s, f, l, lease, true, "", track)
		audioComplete(t, r, s, f, l, lease)
		if after := audioItemSnapshot(t, pool, id); after != before {
			t.Fatal("metadata alternated after original library rescan")
		}
	}
	var joined string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1`, otherF.ID).Scan(&joined); err != nil || joined != id {
		t.Fatalf("join=%s expected=%s err=%v", joined, id, err)
	}
}

func TestNativeAudioRecordsAudiobookProviderIDsDB(t *testing.T) {
	pool, s, f, l, r := nativeAudioFixture(t)
	_, otherS, otherF, otherL, otherR := nativeAudioFixture(t)
	isbn := fmt.Sprintf("978-123-%d", f.ID)
	normalized := normalizeNativeAudioIdentifier(isbn)
	ebook := fmt.Sprintf("ebook-provider-fixture-%d", f.ID)
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_items(content_id,type,title) VALUES($1,'ebook','Fixture')`, ebook); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id=$1`, ebook) })
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_item_provider_ids(content_id,provider,provider_id,item_type) VALUES($1,'isbn',$2,'ebook')`, ebook, normalized); err != nil {
		t.Fatal(err)
	}
	track := audioTestTrack("book", 1, 1)
	track.Entry.Audiobook = &storagev1.AudiobookMetadata{Title: "Shared book", Isbn: isbn}
	track.Probe.Tags["asin"] = fmt.Sprintf(" ab-c-%d ", f.ID)
	other := audioTestTrack("other", 1, 1)
	other.Entry.Audiobook = &storagev1.AudiobookMetadata{Title: "Other title", Isbn: isbn}
	lease := audioBegin(t, r, l)
	otherLease := audioBegin(t, otherR, otherL)
	// Exercise competing item identity locks without calling testing helpers from
	// goroutines. Each publisher runs inside the journal transaction.
	publish := func(repo *storagesource.Repository, scan *Scanner, folder *models.MediaFolder, location storagesource.Location, lease storagesource.Lease, track StorageAudioTrack) error {
		cp, _, err := repo.NextDirectory(t.Context(), lease)
		if err != nil {
			return err
		}
		return repo.ApplyPage(t.Context(), lease, cp, &storagev1.ListResponse{Complete: true, Entries: []*storagev1.Entry{track.Entry}}, func(ctx context.Context, tx pgx.Tx, _ []*storagev1.Entry, _ []string) error {
			_, err := scan.PublishStorageAudioPageTx(ctx, tx, StorageAudioPage{Folder: folder, Location: location, Lease: lease, ConfigurationRevision: lease.ConfigurationRevision, DirectoryID: cp.DirectoryID, Complete: true, Tracks: []StorageAudioTrack{track}})
			return err
		})
	}
	done := make(chan error, 2)
	go func() { done <- publish(r, s, f, l, lease, track) }()
	go func() { done <- publish(otherR, otherS, otherF, otherL, otherLease, other) }()
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	audioComplete(t, r, s, f, l, lease)
	audioComplete(t, otherR, otherS, otherF, otherL, otherLease)
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(DISTINCT content_id) FROM media_files WHERE media_folder_id=ANY($1::int[])`, []int{f.ID, otherF.ID}).Scan(&count); err != nil || count != 1 {
		t.Fatalf("items=%d err=%v", count, err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_item_provider_ids WHERE provider='isbn' AND provider_id=$1`, normalized).Scan(&count); err != nil || count != 2 {
		t.Fatalf("ISBN type rows=%d err=%v", count, err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_item_provider_ids WHERE provider='asin' AND provider_id=$1 AND item_type='audiobook'`, fmt.Sprintf("AB-C-%d", f.ID)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ASIN rows=%d err=%v", count, err)
	}
	// Exercise the owning request reconciler instead of duplicating its identity SQL.
	var uid int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled) VALUES($1,$2,'x','user',true) RETURNING id`, fmt.Sprintf("audio-request-%d", f.ID), fmt.Sprintf("audio-request-%d@example.test", f.ID)).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	requestID := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO bloem_book_requests(id,organization_id,identity_key,title,isbn,asin,status,requested_by_user_id,requested_by_profile_id,client_key,request_digest)
 SELECT $1::uuid,id,$2,'Native audio request',$3,$4,'approved',$5,'fixture-profile',($1::uuid)::text,'fixture-digest' FROM organizations WHERE is_default`, requestID, "isbn:"+normalized, normalized, fmt.Sprintf("AB-C-%d", f.ID), uid); err != nil {
		t.Fatal(err)
	}
	if _, err := bookrequests.NewService(pool, nil).Reconcile(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	var status, matched string
	if err := pool.QueryRow(t.Context(), `SELECT status,library_content_id FROM bloem_book_requests WHERE id=$1`, requestID).Scan(&status, &matched); err != nil {
		t.Fatal(err)
	}
	var nativeID string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1 LIMIT 1`, f.ID).Scan(&nativeID); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || matched != nativeID || matched == ebook {
		t.Fatalf("request status=%s match=%s native=%s", status, matched, nativeID)
	}

}

func TestNativeAudioStagingOverflowRemainsRejectedOnReplayDB(t *testing.T) {
	pool, s, f, l, r := nativeAudioFixture(t)
	lease := audioBegin(t, r, l)
	tracks := make([]StorageAudioTrack, 513)
	for i := range tracks {
		tracks[i] = audioTestTrack(fmt.Sprintf("%03d", i), 1, uint32(i+1))
	}
	audioApply(t, r, s, f, l, lease, false, "next", tracks[:512]...)
	audioApply(t, r, s, f, l, lease, false, "last", tracks[512:]...)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	result, err := s.PublishStorageAudioPageTx(t.Context(), tx, StorageAudioPage{Folder: f, Location: l, Lease: lease, ConfigurationRevision: lease.ConfigurationRevision, DirectoryID: "root", Complete: true, Tracks: tracks[:512]})
	if err != nil || result.BooksRejected != 1 || result.BooksPublished != 0 {
		t.Fatalf("overflow replay result=%+v err=%v", result, err)
	}
}

func TestNativeAudioRetiresRemovedTrackAndHidesIncompletePresentationDB(t *testing.T) {
	pool, s, f, l, r := nativeAudioFixture(t)
	tracks := []StorageAudioTrack{audioTestTrack("1", 1, 1), audioTestTrack("2", 1, 2), audioTestTrack("3", 1, 3)}
	lease := audioBegin(t, r, l)
	audioApply(t, r, s, f, l, lease, true, "", tracks...)
	audioComplete(t, r, s, f, l, lease)
	lease = audioBegin(t, r, l)
	audioApplyWithRemoved(t, r, s, f, l, lease, true, "", []string{"3"}, tracks[:2]...)
	audioComplete(t, r, s, f, l, lease)
	var present, total, retired int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE missing_since IS NULL),COALESCE(max(presentation_part_total) FILTER(WHERE missing_since IS NULL),0),count(*) FILTER(WHERE missing_since IS NOT NULL AND presentation_group_key='') FROM media_files WHERE media_folder_id=$1`, f.ID).Scan(&present, &total, &retired); err != nil || present != 2 || total != 2 || retired != 1 {
		t.Fatalf("present=%d total=%d retired=%d err=%v", present, total, retired, err)
	}
	// A rejected changed revision removes one track's retained authority. The
	// finalizer hides the rest of the old presentation in the same completion.
	tracks[0].Entry.Revision = "r2"
	tracks[0].Status = StorageAudioTrackInvalid
	tracks[0].Probe = nil
	lease = audioBegin(t, r, l)
	audioApply(t, r, s, f, l, lease, true, "", tracks[:2]...)
	audioComplete(t, r, s, f, l, lease)
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files WHERE media_folder_id=$1 AND missing_since IS NULL`, f.ID).Scan(&present); err != nil || present != 0 {
		t.Fatalf("broken presentation present=%d err=%v", present, err)
	}
	tracks[0] = audioTestTrack("1", 1, 1)
	tracks[0].Entry.Revision = "r2"
	lease = audioBegin(t, r, l)
	audioApply(t, r, s, f, l, lease, true, "", tracks[:2]...)
	audioComplete(t, r, s, f, l, lease)
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files WHERE media_folder_id=$1 AND missing_since IS NULL`, f.ID).Scan(&present); err != nil || present != 2 {
		t.Fatalf("restoration present=%d err=%v", present, err)
	}
}

func TestLocalAudiobookScanPreservesPublishedNativeAudioDB(t *testing.T) {
	pool, s, folder, location, r := nativeAudioFixture(t)
	lease := audioBegin(t, r, location)
	audioApply(t, r, s, folder, location, lease, true, "", audioTestTrack("native", 1, 1))
	audioComplete(t, r, s, folder, location, lease)
	var nativeID int
	if err := pool.QueryRow(t.Context(), `SELECT id FROM media_files WHERE media_folder_id=$1`, folder.ID).Scan(&nativeID); err != nil {
		t.Fatal(err)
	}
	before, err := s.fileRepo.GetByID(t.Context(), nativeID)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	book := filepath.Join(root, "Local Book")
	if err := os.Mkdir(book, 0700); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(book, "local.mp3")
	if out, err := exec.CommandContext(t.Context(), "ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "sine=duration=2.75", "-c:a", "libmp3lame", "-y", local).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	folder.Paths = []string{root}
	s.emptyTrashAfterScan = true
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,$2)`, folder.ID, root); err != nil {
		t.Fatal(err)
	}
	if err := s.ScanAudiobookFolder(t.Context(), folder, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	if err := s.ScanAudiobookFolder(t.Context(), folder, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.fileRepo.DeleteMissingByFolder(t.Context(), folder.ID, 0, nil); err != nil {
		t.Fatal(err)
	}
	after, err := s.fileRepo.GetByID(t.Context(), nativeID)
	if err != nil {
		t.Fatal(err)
	}
	if after.MissingSince != nil || after.ContentID != before.ContentID || after.PresentationGroupKey != before.PresentationGroupKey {
		t.Fatalf("native changed: %+v", after)
	}
	var refs, roots, members int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM bloem_storage_file_refs WHERE media_file_id=$1),(SELECT count(*) FROM bloem_storage_file_roots WHERE media_file_id=$1 AND enabled),(SELECT count(*) FROM media_item_libraries WHERE content_id=$2 AND media_folder_id=$3)`, nativeID, before.ContentID, folder.ID).Scan(&refs, &roots, &members); err != nil {
		t.Fatal(err)
	}
	if refs != 1 || roots != 1 || members != 1 {
		t.Fatalf("refs=%d roots=%d membership=%d", refs, roots, members)
	}
}
