package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

type onboardingCallbacks struct{ calls int }

func (p *onboardingCallbacks) CacheAudiobookCover(context.Context, []byte, string) (string, string, error) {
	p.calls++
	return "", "", nil
}
func (p *onboardingCallbacks) CacheEbookCover(context.Context, []byte, string) (string, string, error) {
	p.calls++
	return "", "", nil
}
func (p *onboardingCallbacks) EnqueueMovieFile(context.Context, int) error { p.calls++; return nil }
func (p *onboardingCallbacks) EnqueueSeriesRoot(context.Context, int, string) error {
	p.calls++
	return nil
}
func (p *onboardingCallbacks) Enqueue(context.Context, string, int) error { p.calls++; return nil }
func (p *onboardingCallbacks) ReconcileMissing(context.Context, int, int, int) (int, int, bool, error) {
	p.calls++
	return 0, 0, false, nil
}
func (p *onboardingCallbacks) SyncForFolder(context.Context, int) error       { p.calls++; return nil }
func (p *onboardingCallbacks) SyncInScope(context.Context, int, string) error { p.calls++; return nil }
func (p *onboardingCallbacks) AutoLinkContent(context.Context, string) (string, bool, error) {
	p.calls++
	return "", false, nil
}
func onboardingUnavailable(t *testing.T, err error) {
	t.Helper()
	var admission *catalog.NativeOnboardingError
	if !errors.As(err, &admission) || admission.Code != "native_storage_unavailable" {
		t.Fatalf("want terminal unavailable, got %v", err)
	}
}
func TestNativeOnboardingConfiguredDependencyRefusal(t *testing.T) {
	cb := &onboardingCallbacks{}
	var typedNil *onboardingCallbacks
	var typedStore *blobstore.Filesystem
	configurations := map[string]*Scanner{
		"fileRepo":             {fileRepo: &FileRepository{}},
		"rootSnapshotRepo":     {rootSnapshotRepo: &ScannedRootRepository{}},
		"groupSnapshotRepo":    {groupSnapshotRepo: &ScannedGroupRepository{}},
		"rootOverrideRepo":     {rootOverrideRepo: &MediaRootOverrideRepository{}},
		"groupOverrideRepo":    {groupOverrideRepo: &MediaGroupOverrideRepository{}},
		"identityOverrideRepo": {identityOverrideRepo: &MediaIdentityOverrideRepository{}},
		"locationRepo":         {locationRepo: &ObservedLocationRepository{}},
		"groupLocationRepo":    {groupLocationRepo: &GroupLocationRepository{}},
		"folderRepo":           {folderRepo: &catalog.FolderRepository{}},
		"libraryRepo":          {libraryRepo: &catalog.LibraryItemRepository{}},
		"episodeLibraryRepo":   {episodeLibraryRepo: &catalog.EpisodeLibraryRepository{}},
		"itemRepo":             {itemRepo: &catalog.ItemRepository{}},
		"personRepo":           {personRepo: &catalog.PersonRepository{}},
		"episodeRepo":          {episodeRepo: &catalog.EpisodeRepository{}},
		"extraRepo":            {extraRepo: &catalog.ExtraRepository{}},
		"artworkStore":         {artworkStore: &blobstore.Filesystem{}},
		"imageCacher":          {imageCacher: cb},
		"markerFetcher":        {markerFetcher: func(context.Context, string) *IntroCreditsMarkers { cb.calls++; return nil }},
		"metadataQueue":        {metadataQueue: cb},
		"ebookEnrichmentQueue": {ebookEnrichmentQueue: cb},
		"movieQueueSyncer":     {movieQueueSyncer: cb},
		"seriesQueueSyncer":    {seriesQueueSyncer: cb},
		"literaryWorkLinker":   {literaryWorkLinker: cb},
		"typedNilImage":        {imageCacher: typedNil},
		"typedNilBlob":         {artworkStore: typedStore},
		"typedNilQueue":        {ebookEnrichmentQueue: typedNil},
	}
	// Existing actual EPUB parser fixture, not an admission mock.
	file := writeTestEPUB(t, []string{"ISBN: 978-0-306-40615-7"})
	root := filepath.Dir(file)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range configurations {
		t.Run(name, func(t *testing.T) {
			if s.nonPersistentParser() {
				t.Fatal("writable/callback dependency admitted offline")
			}
			for _, hook := range []string{"folder", "subtree", "file", "ebook"} {
				t.Run(hook, func(t *testing.T) {
					var progress int
					ctx := WithProgressReporter(t.Context(), func(ProgressUpdate) { progress++ })
					f := &models.MediaFolder{ID: 44, Type: "manga", Enabled: true, Paths: []string{root}}
					var err error
					switch hook {
					case "folder":
						_, err = s.ScanFolder(ctx, f)
					case "subtree":
						_, err = s.ScanSubtree(ctx, f, root)
					case "file":
						err = s.ScanFile(ctx, file, f)
					case "ebook":
						f.Type = "ebooks"
						err = s.ScanEbookFolder(ctx, f)
					}
					onboardingUnavailable(t, err)
					if progress != 0 || cb.calls != 0 {
						t.Fatalf("external/progress effects before refusal: %d/%d", cb.calls, progress)
					}
				})
			}
		})
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("parser input mutated")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("unexpected filesystem outputs: %v", entries)
	}
}
func TestNativeOnboardingOfflineIsNotModeAuthority(t *testing.T) {
	s := &Scanner{}
	f, n, err := s.NativeLibraryScanMode(t.Context(), 44)
	onboardingUnavailable(t, err)
	if f != nil || n {
		t.Fatal("offline object claimed durable mode")
	}
	for _, typ := range []string{"movies", "series", "podcasts", "", "invalid"} {
		_, err := s.ScanFolder(t.Context(), &models.MediaFolder{ID: 44, Type: typ, Paths: []string{t.TempDir()}})
		onboardingUnavailable(t, err)
	}
	_, err = s.ScanFolder(t.Context(), &models.MediaFolder{Type: "audiobooks", Paths: []string{t.TempDir()}})
	onboardingUnavailable(t, err) // Full audio success would unconditionally need a repair tx.
}
func TestNativeOnboardingInvalidConfiguredAndNilAdmission(t *testing.T) {
	for _, id := range []int{-1, 0, 44} {
		s := &Scanner{fileRepo: &FileRepository{}}
		_, err := s.ScanFolder(t.Context(), &models.MediaFolder{ID: id, Type: "ebooks"})
		onboardingUnavailable(t, err)
	}
	var s *Scanner
	_, err := s.ScanFolder(t.Context(), &models.MediaFolder{ID: 44, Type: "ebooks"})
	onboardingUnavailable(t, err)
	_, err = (&Scanner{}).ScanFolder(t.Context(), nil)
	onboardingUnavailable(t, err)
	_, err = (&Scanner{}).ScanFolder(nil, &models.MediaFolder{ID: 44, Type: "ebooks"}) //nolint:staticcheck // SA1012: Intentional nil context verifies admission refuses missing authority.
	onboardingUnavailable(t, err)
}
func TestNativeOnboardingRealConstructorCannotBeOffline(t *testing.T) {
	s := NewScanner(&FileRepository{}, "", nil, 1, false, 0)
	if s.nonPersistentParser() {
		t.Fatal("live constructor admitted offline")
	}
	if s.folderRepo == nil || s.fileRepo == nil || s.extraRepo == nil {
		t.Fatal("constructor lacks expected repositories")
	}
	_, err := s.ScanFolder(t.Context(), &models.MediaFolder{ID: 44, Type: "ebooks", Paths: []string{t.TempDir()}})
	onboardingUnavailable(t, err)
}

type onboardingCarriedTx struct{ pgx.Tx }

func TestNativeOnboardingOfflineRejectsCarriedRepairTransaction(t *testing.T) {
	var typedNil *onboardingCarriedTx
	for _, tx := range []pgx.Tx{&onboardingCarriedTx{}, typedNil} {
		ctx := withRepairTx(t.Context(), tx)
		_, err := (&Scanner{}).ScanFolder(ctx, &models.MediaFolder{ID: 44, Type: "ebooks", Paths: []string{t.TempDir()}})
		onboardingUnavailable(t, err)
	}
}
