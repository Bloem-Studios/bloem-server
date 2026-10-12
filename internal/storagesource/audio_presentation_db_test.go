//go:build integration

package storagesource

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAudioStagingCascadesWithRunAndLocationDB(t *testing.T) {
	source, r := fixtureSource(t, storageTestPool(t))
	fixtureFolder(t, r.pool, 93001)
	location := fixtureLocation(t, r, source.Key, 93001)
	lease, err := r.Begin(t.Context(), source.Key, "audio-staging", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	track := StagedAudioTrack{EntryID: "track", Revision: "r1", Status: "ok", Entry: []byte{1}, Probe: []byte(`{"duration_seconds":2}`)}
	if err := StageAudioTracksTx(t.Context(), tx, lease, "root", []StagedAudioTrack{track}); err != nil {
		t.Fatal(err)
	}
	tracks, err := StagedAudioTracksTx(t.Context(), tx, lease, "root")
	if err != nil || len(tracks) != 1 {
		t.Fatalf("staging=%v err=%v", tracks, err)
	}
	if _, err := tx.Exec(t.Context(), `UPDATE bloem_storage_sources SET discovery_run_id=NULL WHERE key=$1`, source.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `DELETE FROM bloem_storage_scan_runs WHERE id=$1`, lease.RunID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_audio_staged_tracks`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("run cascade count=%d err=%v", count, err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	lease, err = r.Begin(t.Context(), source.Key, "audio-staging", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	tx, err = r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err := StageAudioTracksTx(t.Context(), tx, lease, "root", []StagedAudioTrack{track}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `DELETE FROM library_storage_locations WHERE id=$1`, location.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_audio_staged_tracks`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("location cascade count=%d err=%v", count, err)
	}
}

func TestPresentationReferencesRequireEveryTrackDB(t *testing.T) {
	for _, mutation := range []string{"none", "membership", "root", "source", "installation", "configuration", "missing", "duplicate index", "total", "malformed path"} {
		t.Run(mutation, func(t *testing.T) {
			r, source, location, ref := fixtureReference(t)
			key, err := AudioPresentationKey(location.ID, "root")
			if err != nil {
				t.Fatal(err)
			}
			execSQL(t, r.pool, `UPDATE media_items SET type='audiobook' WHERE content_id='existing-book'`)
			execSQL(t, r.pool, `UPDATE media_files SET presentation_kind='multipart',presentation_group_key=$1,presentation_part_index=1,presentation_part_total=2 WHERE id=91001`, key)
			if err := attachFile(t, r, 1, 91001, ref); err != nil {
				t.Fatal(err)
			}
			ref.EntryID = "track2"
			ref.LogicalPath = "Book/2.mp3"
			path, err := CatalogLocation(location.ID, ref.EntryID)
			if err != nil {
				t.Fatal(err)
			}
			execSQL(t, r.pool, `INSERT INTO media_files(id,content_id,media_folder_id,file_path,presentation_kind,presentation_group_key,presentation_part_index,presentation_part_total) VALUES(91002,'existing-book',91001,$1,'multipart',$2,2,2)`, path, key)
			if err := attachFile(t, r, 1, 91002, ref); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "membership":
				execSQL(t, r.pool, `UPDATE bloem_storage_file_roots SET enabled=false WHERE media_file_id=91002`)
			case "root":
				execSQL(t, r.pool, `UPDATE bloem_storage_selected_roots SET enabled=false WHERE location_id=$1`, location.ID)
			case "source":
				execSQL(t, r.pool, `UPDATE bloem_storage_sources SET enabled=false WHERE key=$1`, source.Key)
			case "installation":
				execSQL(t, r.pool, `UPDATE plugin_installations SET enabled=false WHERE id=91001`)
			case "configuration":
				execSQL(t, r.pool, `UPDATE bloem_storage_file_refs SET configuration_revision=2 WHERE media_file_id=91002`)
			case "missing":
				execSQL(t, r.pool, `UPDATE media_files SET missing_since=clock_timestamp() WHERE id=91002`)
			case "duplicate index":
				execSQL(t, r.pool, `UPDATE media_files SET presentation_part_index=1 WHERE id=91002`)
			case "total":
				execSQL(t, r.pool, `UPDATE media_files SET presentation_part_total=3 WHERE id=91002`)
			case "malformed path":
				execSQL(t, r.pool, `UPDATE media_files SET file_path='bloem-storage://malformed' WHERE id=91002`)
			}
			ids, err := r.PresentationReferences(t.Context(), 91001, "existing-book", key)
			if mutation == "none" {
				if err != nil || len(ids) != 2 || ids[0] != 91001 || ids[1] != 91002 {
					t.Fatalf("ids=%v err=%v", ids, err)
				}
				return
			}
			want := ErrReferenceConflict
			if mutation == "source" || mutation == "installation" {
				want = ErrSourceUnavailable
			}
			if !errors.Is(err, want) {
				t.Fatalf("ids=%v err=%v want=%v", ids, err, want)
			}
		})
	}
}

func TestAudioStagingOtherRunCleanupAndCrossLocationDB(t *testing.T) {
	source, r := fixtureSource(t, storageTestPool(t))
	fixtureFolder(t, r.pool, 93001)
	location := fixtureLocation(t, r, source.Key, 93001)
	lease, err := r.Begin(t.Context(), source.Key, "audio-staging", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	track := StagedAudioTrack{EntryID: "track", Revision: "r1", Status: "ok", Entry: []byte{1}, Probe: []byte(`{"duration_seconds":2}`)}
	tx, err := r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if err := StageAudioTracksTx(t.Context(), tx, lease, "root", []StagedAudioTrack{track}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	execSQL(t, r.pool, `UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, lease.RunID)
	execSQL(t, r.pool, `UPDATE library_storage_locations SET selection_revision=selection_revision+1 WHERE id=$1`, location.ID)
	next, err := r.Begin(t.Context(), source.Key, "audio-staging", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if next.RunID == lease.RunID {
		t.Fatal("selection change reused old run")
	}
	tx, err = r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	count, err := DeleteStaleAudioStagingTx(t.Context(), tx, next)
	if err != nil || count != 1 {
		t.Fatalf("cleanup=%d err=%v", count, err)
	}
	if err := StageAudioTracksTx(t.Context(), tx, next, "root", []StagedAudioTrack{track}); err != nil {
		t.Fatal(err)
	}
	count, err = DeleteStaleAudioStagingTx(t.Context(), tx, next)
	if err != nil || count != 0 {
		t.Fatalf("current staging deleted=%d err=%v", count, err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	fixtureFolder(t, r.pool, 93002)
	other := fixtureLocation(t, r, source.Key, 93002)
	tx, err = r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	_, err = tx.Exec(t.Context(), `INSERT INTO bloem_storage_audio_staged_tracks(run_id,directory_id,entry_id,location_id,revision,status,entry) VALUES($1,'root','forged',$2,'r1','invalid',$3)`, next.RunID, other.ID, []byte{1})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err == nil {
		t.Fatal("composite run/location FK accepted other location")
	}
}
