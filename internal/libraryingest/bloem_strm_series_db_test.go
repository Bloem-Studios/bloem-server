//go:build integration

package libraryingest

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/remotesource"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

const (
	strmSeriesC01 = "television/10s/Show C (2015) {tvdb-333}/Season 01/Show C (2015) - S01E01"
	strmSeriesC02 = "television/10s/Show C (2015) {tvdb-333}/Season 01/Show C (2015) - S01E02"
)

// strmRemote serves the test video and records the Range header of every
// request it answers.
type strmRemote struct {
	url    string
	mu     sync.Mutex
	ranges []string
}

func newStrmRemote(t *testing.T) *strmRemote {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "scanner", "testdata", "test.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	remote := &strmRemote{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remote.mu.Lock()
		remote.ranges = append(remote.ranges, r.Header.Get("Range"))
		remote.mu.Unlock()
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "remote.mp4", time.Unix(1_700_000_000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(server.Close)
	remote.url = server.URL + "/shows/c01.mp4?token=secret"
	return remote
}

func (r *strmRemote) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.ranges
	r.ranges = nil
	return out
}

// strmSidecarAuthority authorizes exactly file, as a viewer's playback does.
func strmSidecarAuthority(file *models.MediaFile) nativestorage.PlaybackAuthority {
	return nativestorage.PlaybackAuthority{AuthorizeFile: func(_ context.Context, m *models.MediaFile) (*models.MediaFile, error) {
		if m.ID != file.ID {
			return nil, catalog.ErrItemNotFound
		}
		actual := *file
		return &actual, nil
	}, AuthorizeSource: func(context.Context, *models.MediaFile, storagesource.SourceConfig) error { return nil }}
}

// readStrmSidecar reads a sidecar through the provider under file's authority.
func readStrmSidecar(t *testing.T, coordinator *nativestorage.Coordinator, file *models.MediaFile, sidecar storagesource.Sidecar) string {
	t.Helper()
	sub, err := coordinator.OpenAuthorizedSidecar(t.Context(), file, strmSidecarAuthority(file), sidecar.PersistedRef)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()
	data, err := io.ReadAll(sub)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// strmEpisodePublished checks the published `.strm` episode and its remote
// source, and that the unreadable one was skipped alone.
func strmEpisodePublished(t *testing.T, f nativeSeriesScanFixture, rt *remotesource.Runtime, remote *strmRemote) (*models.MediaFile, remotesource.Source) {
	t.Helper()
	ctx := t.Context()
	file, err := scanner.NewFileRepository(f.pool).GetByID(ctx, seriesFileID(t, f, strmSeriesC01+".strm"))
	if err != nil || file == nil {
		t.Fatal(err)
	}
	var title string
	if err := f.pool.QueryRow(ctx, `SELECT i.title FROM media_items i WHERE i.content_id=$1`, file.ContentID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if file.BaseType != "series" || file.SeasonNumber != 1 || file.EpisodeNumber != 1 || file.Container != "mp4" || file.CodecVideo != "h264" || title != "Show C" {
		t.Fatalf("published .strm episode %+v (series %q)", file, title)
	}
	source, ok, err := rt.Store.Get(ctx, file.FilePath)
	if err != nil || !ok || source.URL != remote.url || source.SourceRevision != "version:one" {
		t.Fatalf("remote source = %+v, %v, %v", source, ok, err)
	}
	var unreadable, otherEpisodes int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE entry_id=$2), count(*) FILTER (WHERE entry_id<>$2)
		FROM bloem_storage_file_refs WHERE location_id=$1`, f.location.ID, strmSeriesC02+".strm").Scan(&unreadable, &otherEpisodes); err != nil {
		t.Fatal(err)
	}
	if unreadable != 0 || otherEpisodes != 5 {
		t.Fatalf("published the unreadable .strm %d times, %d other episodes", unreadable, otherEpisodes)
	}
	return file, source
}

// A `.strm` object in a TV library's storage source is the episode its name
// says, probed from its URL, with the subtitle file beside it. An unreadable
// `.strm` episode skips only itself. A rescan of the unchanged object costs
// one one-byte request. The object opens for playback at the revision the URL
// was read from, which the `.strm` playback requires, and its sidecar reads
// through the provider under the episode's authority.
func TestNativeStrmStorageSeriesEpisodesDB(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeSeriesScanFixture(t, binary, manifest, true)
	remote := newStrmRemote(t)
	if err := os.WriteFile(f.notify+".strm-url", []byte(remote.url+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.Registry.ReplaceConfiguration(t.Context(), f.installed.Source.Key, f.installed.Source.OwnerID, map[string]map[string]any{"source": {"mode": "series", "notify": f.notify, "revision": "version:one"}}); err != nil {
		t.Fatal(err)
	}
	rt := &remotesource.Runtime{Store: remotesource.NewStore(f.pool), Guard: remotesource.NewGuard(remotesource.Options{AllowPrivate: func(context.Context) bool { return true }})}
	f.scan.ConfigureRemoteSources(rt)

	f.scanOnce(t)
	if got := remote.take(); !slices.Contains(got, "bytes=0-1048575") {
		t.Fatalf("the first scan must probe the remote episode: %q", got)
	}
	ctx := t.Context()
	file, source := strmEpisodePublished(t, f, rt, remote)
	fileID := file.ID

	sources := storagesource.NewRepository(f.pool)
	sidecars, err := sources.FileSidecars(ctx, fileID)
	if err != nil || len(sidecars) != 1 || sidecars[0].EntryID != strmSeriesC01+".en.srt" || sidecars[0].Language != "en" || sidecars[0].Format != "srt" {
		t.Fatalf(".strm episode sidecars %+v %v", sidecars, err)
	}
	coordinator := &nativestorage.Coordinator{References: sources, Registry: f.host.Registry, Runtime: nativestorage.ManagedRuntime{Manager: f.host.Manager}}
	opened, err := coordinator.OpenAuthorizedPlayback(ctx, file, strmSidecarAuthority(file))
	if err != nil {
		t.Fatal(err)
	}
	if revision := opened.Identity().Reference.Revision; revision != source.SourceRevision {
		t.Fatalf("playback opens revision %q, the URL was read from %q", revision, source.SourceRevision)
	}
	_ = opened.Close()
	if data := readStrmSidecar(t, coordinator, file, sidecars[0]); data != "1\n00:00:00,000 --> 00:00:01,000\nhi Show C (2015) - S01E01.en.srt\n" {
		t.Fatalf("sidecar read %q", data)
	}

	f.scanOnce(t)
	if got := remote.take(); len(got) != 1 || got[0] != "bytes=0-0" {
		t.Fatalf("an unchanged .strm episode asked the remote for %q, want one bytes=0-0", got)
	}
	if again := seriesFileID(t, f, strmSeriesC01+".strm"); again != fileID {
		t.Fatalf("rescan replaced the episode file %d with %d", fileID, again)
	}
	if sidecars, err := sources.FileSidecars(ctx, fileID); err != nil || len(sidecars) != 1 {
		t.Fatalf("rescan sidecars %+v %v", sidecars, err)
	}
}

// A subtitle file beside a `.strm` object in a movie library's storage
// source is recorded for that movie and reads through the provider.
func TestNativeStrmStorageMovieSidecarDB(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeMovieScanFixture(t, binary, manifest, true, "movies")
	remote := newStrmRemote(t)
	if err := os.WriteFile(f.notify+".strm-url", []byte(remote.url+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.ReplaceConfiguration(t.Context(), f.installed.Source.Key, f.installed.Source.OwnerID, map[string]map[string]any{"source": {"mode": "movies", "notify": f.notify, "revision": "version:strm"}}); err != nil {
		t.Fatal(err)
	}
	f.scan.ConfigureRemoteSources(strmTestRuntime(f))
	if _, err := f.scan.ScanStorageFolder(t.Context(), f.folder, storagesource.LocationSelection{Location: f.location}); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	var fileID int
	if err := f.pool.QueryRow(ctx, `SELECT media_file_id FROM bloem_storage_file_refs WHERE location_id=$1 AND entry_id='03-shortcut'`, f.location.ID).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	file, err := scanner.NewFileRepository(f.pool).GetByID(ctx, fileID)
	if err != nil || file == nil {
		t.Fatal(err)
	}
	sources := storagesource.NewRepository(f.pool)
	sidecars, err := sources.FileSidecars(ctx, fileID)
	if err != nil || len(sidecars) != 1 || sidecars[0].EntryID != "04-shortcut-srt" || sidecars[0].Language != "en" {
		t.Fatalf(".strm movie sidecars %+v %v", sidecars, err)
	}
	host := f.scan.host
	coordinator := &nativestorage.Coordinator{References: sources, Registry: host.Registry, Runtime: nativestorage.ManagedRuntime{Manager: host.Manager}}
	if data := readStrmSidecar(t, coordinator, file, sidecars[0]); data != "1\n00:00:00,000 --> 00:00:01,000\nhi remote.en.srt\n" {
		t.Fatalf("sidecar read %q", data)
	}
}
