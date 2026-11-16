package downloads

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type nativePrepareMarker struct{}

type nativePreparationFixture struct {
	refuse    map[string]bool
	requested []string
	ended     int
}

func (n *nativePreparationFixture) DownloadFile(_ context.Context, file *models.MediaFile) (*models.MediaFile, bool) {
	view := *file
	view.FileSize = 4096
	return &view, true
}

func (n *nativePreparationFixture) BeginPreparation(ctx context.Context, artifactID, attemptID string, requester *Download, _ *models.MediaFile) (context.Context, func(), error) {
	n.requested = append(n.requested, requester.ID)
	if n.refuse[requester.ID] || requester.ArtifactID != artifactID || attemptID == "" {
		return nil, nil, errors.New("viewer may not read the source")
	}
	return context.WithValue(ctx, nativePrepareMarker{}, requester.ID), func() { n.ended++ }, nil
}

type markerPreparer struct {
	calls  int
	marker any
}

func (p *markerPreparer) PrepareFile(ctx context.Context, _ string, _ playback.TranscodeOpts, outputPath string) (PreparedArtifact, error) {
	p.calls++
	p.marker = ctx.Value(nativePrepareMarker{})
	return PreparedArtifact{OutputPath: outputPath, FileSize: 10}, nil
}

func installNativePreparation(t *testing.T, m *ArtifactManager, native NativePreparation) {
	t.Helper()
	files, ok := m.fileRepo.(*nativeArtifactFiles)
	if !ok {
		t.Fatal("artifact manager does not resolve native files")
	}
	files.native.Store(&nativePreparationBox{native})
}

// A prepared copy of a native video reads the source for a download waiting
// for it: the oldest whose viewer may still read it. A job no download may
// read for fails, and the preparer never sees the source.
func TestNativePreparationReadsForWaitingDownloadsDB(t *testing.T) {
	f := seedManagedFixture(t)
	ctx := context.Background()
	artifacts := NewArtifactRepository(f.pool)
	a, _, err := artifacts.EnsureQueued(ctx, newArtifact(t, f.fileID, fmt.Sprintf("native-prepare-%d", time.Now().UnixNano())))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = f.pool.Exec(context.Background(), `DELETE FROM downloads WHERE id = $1`, id)
		}
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM download_artifacts WHERE id = $1`, a.ID)
	})
	start := time.Now().Add(-time.Minute)
	for i, status := range []string{StatusPreparing, StatusPreparing, StatusCancelled} {
		id := fmt.Sprintf("native-prep-%d-%d", time.Now().UnixNano(), i)
		created := start.Add(time.Duration(i) * time.Second)
		if err := f.repo.Create(ctx, &Download{ID: id, UserID: f.userID, ProfileID: f.profileA, MediaFileID: f.fileID, ContentID: f.contentID,
			Kind: KindQueued, Status: status, Format: FormatTranscode, ArtifactID: a.ID, CreatedAt: created, UpdatedAt: created}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	file := &models.MediaFile{ID: f.fileID, FilePath: "bloem-storage:location/entry"}
	opts := playback.TranscodeOpts{InputPath: file.FilePath}
	attempt := a.ID + "-6f1b7a64-3a60-4f0c-9c1a-3d8e0f5b2c11"

	preparer := &markerPreparer{}
	m := NewArtifactManager(artifacts, f.repo, fakeFileResolver{file: file}, preparer, "native-prepare-test", nil, nil)
	native := &nativePreparationFixture{refuse: map[string]bool{ids[0]: true}}
	installNativePreparation(t, m, native)
	prepared, err := m.bloemPrepareFile(ctx, attempt, file, a, opts)
	if err != nil || prepared.FileSize != 10 {
		t.Fatalf("prepare = %+v, %v", prepared, err)
	}
	if preparer.calls != 1 || preparer.marker != ids[1] || native.ended != 1 {
		t.Fatalf("preparer calls=%d marker=%v ended=%d", preparer.calls, preparer.marker, native.ended)
	}
	if len(native.requested) != 2 || native.requested[0] != ids[0] || native.requested[1] != ids[1] {
		t.Fatalf("requesters tried = %v; want the two waiting downloads, oldest first", native.requested)
	}

	native.refuse[ids[1]] = true
	if _, err := m.bloemPrepareFile(ctx, attempt, file, a, opts); !errors.Is(err, errNativePreparationRefused) || preparer.calls != 1 {
		t.Fatalf("all refused: err=%v preparer calls=%d", err, preparer.calls)
	}

	local := &models.MediaFile{ID: f.fileID, FilePath: "/media/movie.mkv"}
	if _, err := m.bloemPrepareFile(ctx, attempt, local, a, playback.TranscodeOpts{InputPath: local.FilePath}); err != nil || preparer.calls != 2 || preparer.marker != nil {
		t.Fatalf("local file: err=%v calls=%d marker=%v", err, preparer.calls, preparer.marker)
	}
}

// Without a preparation a native job fails instead of handing the reference
// to ffmpeg, and a native file resolves to its download view only once a
// preparation is installed.
func TestNativePreparationNeedsComposition(t *testing.T) {
	file := &models.MediaFile{ID: 5, FilePath: "/media/Remote.strm", FileSize: 120}
	preparer := &markerPreparer{}
	m := NewArtifactManager(nil, nil, fakeFileResolver{file: file}, preparer, "native-prepare-test", nil, nil)
	a := &Artifact{ID: "artifact", OutputPath: "/tmp/artifact.mp4"}
	if _, err := m.bloemPrepareFile(t.Context(), "artifact-6f1b7a64-3a60-4f0c-9c1a-3d8e0f5b2c11", file, a, playback.TranscodeOpts{InputPath: file.FilePath}); !errors.Is(err, errNativePreparationRefused) || preparer.calls != 0 {
		t.Fatalf("uncomposed: err=%v calls=%d", err, preparer.calls)
	}
	if got, _ := m.fileRepo.GetByID(t.Context(), 5); got.FileSize != 120 {
		t.Fatalf("view before composition = %d", got.FileSize)
	}
	installNativePreparation(t, m, &nativePreparationFixture{})
	if got, _ := m.fileRepo.GetByID(t.Context(), 5); got.FileSize != 4096 {
		t.Fatalf("view after composition = %d; want the served size", got.FileSize)
	}
}

// reservingPlanner offers a transcode node for every job.
type reservingPlanner struct{ reserved int }

func (p *reservingPlanner) ReserveTranscodeWork(string) (*nodepool.Node, func()) {
	p.reserved++
	return &nodepool.Node{ID: 7, URL: "http://192.0.2.7:8080"}, func() {}
}
func (p *reservingPlanner) TranscodeNode(int) (*nodepool.Node, bool) { return nil, false }

type fallbackSettings map[string]string

func (s fallbackSettings) GetAll(context.Context) (map[string]string, error) { return s, nil }

// A native preparation runs on the API server even when a transcode node is
// free: a node reads its sources only through a core source grant, which
// cannot name a native input. The pooled preparer's local fallback setting
// still applies, so with it off the job fails rather than going to a node.
func TestNativePreparationRunsOnTheAPIServer(t *testing.T) {
	local, planner := &markerPreparer{}, &reservingPlanner{}
	pooled := NewNodeAwarePreparer(local, planner, nil)
	m := &ArtifactManager{preparer: pooled}
	opts := playback.TranscodeOpts{InputPath: "bloem-storage:location/entry"}
	if _, err := m.prepareNativeHere(t.Context(), "artifact-6f1b7a64-3a60-4f0c-9c1a-3d8e0f5b2c11", opts, "/tmp/artifact.mp4"); err != nil {
		t.Fatal(err)
	}
	if local.calls != 1 || planner.reserved != 0 {
		t.Fatalf("local calls=%d node reservations=%d; want it prepared here", local.calls, planner.reserved)
	}
	pooled.SetSettingsReader(fallbackSettings{config.DownloadLocalTranscodeFallbackSettingKey: "false"})
	if _, err := m.prepareNativeHere(t.Context(), "artifact-6f1b7a64-3a60-4f0c-9c1a-3d8e0f5b2c11", opts, "/tmp/artifact.mp4"); err == nil || local.calls != 1 || planner.reserved != 0 {
		t.Fatalf("fallback off: err=%v local calls=%d node reservations=%d", err, local.calls, planner.reserved)
	}
}
