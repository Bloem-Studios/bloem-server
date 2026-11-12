//go:build integration

package libraryingest

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func seriesFileID(t *testing.T, f nativeSeriesScanFixture, entry string) int {
	t.Helper()
	var id int
	if err := f.pool.QueryRow(t.Context(), `SELECT media_file_id FROM bloem_storage_file_refs WHERE location_id=$1 AND entry_id=$2`, f.location.ID, entry).Scan(&id); err != nil {
		t.Fatal(entry, err)
	}
	return id
}

// The sidecars the series scan records are listed for their episode, with
// language and flags, and read through the storage plugin at their recorded
// revision, only under the episode's playback authority. A new revision is
// never answered with the old bytes, and an absent entry is not listed.
func TestNativeSeriesSidecarsReadThroughProviderDB(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeSeriesScanFixture(t, binary, manifest, true)
	f.scanOnce(t)
	ctx := t.Context()
	sources := storagesource.NewRepository(f.pool)
	fileID := seriesFileID(t, f, seriesA01+".mkv")
	file, err := scanner.NewFileRepository(f.pool).GetByID(ctx, fileID)
	if err != nil || file == nil {
		t.Fatal(err)
	}

	sidecars, err := sources.FileSidecars(ctx, fileID)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		suffix, language string
		sdh              bool
	}{{".en.sdh.srt", "en", true}, {".en.srt", "en", false}, {".nl.srt", "nl", false}}
	if len(sidecars) != len(want) {
		t.Fatalf("sidecars %+v", sidecars)
	}
	for i, w := range want {
		s := sidecars[i]
		entry := seriesA01 + w.suffix
		if s.MediaFileID != fileID || s.LocationID != f.location.ID || s.EntryID != entry || s.LogicalPath != entry || s.Name != path.Base(entry) || s.Revision != "version:one" ||
			s.Format != "srt" || s.Language != w.language || s.HearingImpaired != w.sdh || s.Forced {
			t.Fatalf("sidecar %d: %+v", i, s)
		}
	}

	coordinator := &nativestorage.Coordinator{References: sources, Registry: f.host.Registry, Runtime: nativestorage.ManagedRuntime{Manager: f.host.Manager}}
	var revoked atomic.Bool
	authority := nativestorage.PlaybackAuthority{AuthorizeFile: func(_ context.Context, m *models.MediaFile) (*models.MediaFile, error) {
		if revoked.Load() || m.ID != file.ID {
			return nil, catalog.ErrItemNotFound
		}
		actual := *file
		return &actual, nil
	}, AuthorizeSource: func(context.Context, *models.MediaFile, storagesource.SourceConfig) error {
		if revoked.Load() {
			return catalog.ErrItemNotFound
		}
		return nil
	}}
	read := func(ref storagesource.PersistedRef) ([]byte, error) {
		opened, err := coordinator.OpenAuthorizedSidecar(ctx, file, authority, ref)
		if err != nil {
			return nil, err
		}
		defer opened.Close()
		return io.ReadAll(opened)
	}
	for _, s := range sidecars {
		data, err := read(s.PersistedRef)
		if err != nil || string(data) != "1\n00:00:00,000 --> 00:00:01,000\nhi "+s.Name+"\n" {
			t.Fatalf("read %s: %q %v", s.EntryID, data, err)
		}
	}

	// Another episode's sidecar never opens under this episode.
	other, err := sources.FileSidecars(ctx, seriesFileID(t, f, seriesA02+".mkv"))
	if err != nil || len(other) != 1 {
		t.Fatalf("other sidecars %+v %v", other, err)
	}
	if data, err := read(other[0].PersistedRef); !errors.Is(err, storagesource.ErrReferenceConflict) || data != nil {
		t.Fatalf("foreign sidecar: %q %v", data, err)
	}

	// Refused authority reaches no provider.
	before := len(seriesAudit(t, f))
	revoked.Store(true)
	if data, err := read(sidecars[0].PersistedRef); err == nil || data != nil {
		t.Fatalf("revoked read: %q %v", data, err)
	}
	if after := seriesAudit(t, f)[before:]; strings.Contains(after, ".srt") {
		t.Fatalf("revoked read touched the provider: %s", after)
	}
	revoked.Store(false)

	// The scan records a new revision of the SDH sidecar: the old pin is
	// refused, and the new revision is read only if the provider holds it.
	sdh := sidecars[0]
	if _, err := f.pool.Exec(ctx, `UPDATE bloem_storage_location_entries SET revision='version:two' WHERE location_id=$1 AND entry_id=$2`, f.location.ID, sdh.EntryID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE bloem_storage_episode_sidecars SET revision='version:two' WHERE location_id=$1 AND entry_id=$2`, f.location.ID, sdh.EntryID); err != nil {
		t.Fatal(err)
	}
	if data, err := read(sdh.PersistedRef); !errors.Is(err, storagesource.ErrReferenceConflict) || data != nil {
		t.Fatalf("stale revision: %q %v", data, err)
	}
	current, err := sources.FileSidecars(ctx, fileID)
	if err != nil || len(current) != 3 || current[0].EntryID != sdh.EntryID || current[0].Revision != "version:two" {
		t.Fatalf("relisted %+v %v", current, err)
	}
	if data, err := read(current[0].PersistedRef); err == nil || data != nil {
		t.Fatalf("revision the provider does not hold: %q %v", data, err)
	}

	// An entry the location no longer holds is not listed.
	if _, err := f.pool.Exec(ctx, `UPDATE bloem_storage_location_entries SET absence_confirmed_at=now() WHERE location_id=$1 AND entry_id=$2`, f.location.ID, seriesA01+".nl.srt"); err != nil {
		t.Fatal(err)
	}
	if current, err = sources.FileSidecars(ctx, fileID); err != nil || len(current) != 2 {
		t.Fatalf("absent entry listed: %+v %v", current, err)
	}
	if _, err := sources.FileSidecar(ctx, fileID, f.location.ID, seriesA01+".nl.srt"); !errors.Is(err, storagesource.ErrReferenceConflict) {
		t.Fatalf("absent entry resolved: %v", err)
	}
}
