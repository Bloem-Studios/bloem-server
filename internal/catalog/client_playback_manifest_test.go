package catalog

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type manifestFixture struct {
	files  []*models.MediaFile
	items  []*models.MediaItem
	filter AccessFilter
	calls  int
	err    error
}

func (f *manifestFixture) loadClientPlaybackFiles(context.Context, int) ([]*models.MediaFile, error) {
	f.calls++
	return f.files, f.err
}
func (f *manifestFixture) GetByIDsWithAccess(_ context.Context, _ []string, filter AccessFilter) ([]*models.MediaItem, error) {
	f.filter = filter
	return f.items, f.err
}
func manifestTestFile() *models.MediaFile {
	return &models.MediaFile{ID: 7, ContentID: "book", MediaFolderID: 2, Duration: 120, ProbeSource: "local"}
}
func manifestTestScope() access.Scope {
	return access.Scope{UserID: 3, ProfileID: "viewer", ProfileVerified: true}
}
func resolveManifestTest(t *testing.T, fixture *manifestFixture, scope access.Scope) (playback.ClientPlaybackManifestV3, error) {
	t.Helper()
	resolver := &ClientPlaybackManifestResolver{items: fixture, files: fixture}
	return resolver.ResolveClientPlaybackManifest(access.SetScope(t.Context(), scope), 3, "viewer", 7)
}
func TestClientPlaybackManifestSingleton(t *testing.T) {
	f := &manifestFixture{files: []*models.MediaFile{manifestTestFile()}, items: []*models.MediaItem{{ContentID: "book", Type: "audiobook"}}}
	manifest, err := resolveManifestTest(t, f, manifestTestScope())
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	selected, err := manifest.SelectFile(7)
	if err != nil || selected.PartOffsetSeconds != 0 || selected.PartDurationSeconds != 120 || selected.DurationSeconds != 120 {
		t.Fatalf("selection = %+v, %v", selected, err)
	}
	if f.filter.AllowedLibraryIDs != nil {
		t.Fatal("nil library policy became restricted")
	}
	original := manifest.TimelineID
	f.files[0].Duration++
	changed, err := resolveManifestTest(t, f, manifestTestScope())
	if err != nil || changed.TimelineID == original {
		t.Fatalf("duration edit did not change digest: %v", err)
	}
}
func TestClientPlaybackManifestAccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*access.Scope)
		denied bool
	}{
		{"nil unrestricted", func(*access.Scope) {}, false},
		{"empty denied", func(s *access.Scope) { s.AllowedLibraryIDs = []int{} }, true},
		{"allowed", func(s *access.Scope) { s.AllowedLibraryIDs = []int{2} }, false},
		{"other library", func(s *access.Scope) { s.AllowedLibraryIDs = []int{8} }, true},
		{"disabled", func(s *access.Scope) { s.DisabledLibraryIDs = []int{2} }, true},
		{"wrong account", func(s *access.Scope) { s.UserID = 4 }, true},
		{"wrong profile", func(s *access.Scope) { s.ProfileID = "other" }, true},
		{"unverified", func(s *access.Scope) { s.ProfileVerified = false }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &manifestFixture{files: []*models.MediaFile{manifestTestFile()}, items: []*models.MediaItem{{ContentID: "book", Type: "audiobook"}}}
			scope := manifestTestScope()
			tc.change(&scope)
			_, err := resolveManifestTest(t, f, scope)
			if tc.denied != errors.Is(err, ErrItemNotFound) {
				t.Fatalf("denied=%v, err=%v", tc.denied, err)
			}
			if !tc.denied && !reflect.DeepEqual(f.filter.AllowedLibraryIDs, scope.AllowedLibraryIDs) {
				t.Fatal("policy changed")
			}
		})
	}
	t.Run("catalog rating denial", func(t *testing.T) {
		f := &manifestFixture{files: []*models.MediaFile{manifestTestFile()}}
		scope := manifestTestScope()
		scope.MaxContentRating = "PG"
		_, err := resolveManifestTest(t, f, scope)
		if !errors.Is(err, ErrItemNotFound) || f.filter.MaxContentRating != "PG" {
			t.Fatalf("catalog authorization not preserved: %v", err)
		}
	})
}
func TestClientPlaybackManifestRefusesUntrustedMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*manifestFixture)
	}{
		{"not audiobook", func(f *manifestFixture) { f.items[0].Type = "movie" }},
		{"unknown duration", func(f *manifestFixture) { f.files[0].Duration = 0 }},
		{"negative duration", func(f *manifestFixture) { f.files[0].Duration = -1 }},
		{"external duration", func(f *manifestFixture) { f.files[0].ProbeSource = "arrs" }},
		{"missing anchor", func(f *manifestFixture) { f.files[0].MissingSince = new(time.Now()) }},
		{"wrong item", func(f *manifestFixture) { f.items[0].ContentID = "other" }},
		{"part without siblings", func(f *manifestFixture) { f.files[0].PresentationPartIndex = 1; f.files[0].PresentationPartTotal = 2 }},
		{"filename sorted multipart", func(f *manifestFixture) {
			a := *f.files[0]
			a.ID = 8
			a.PresentationPartIndex = 2
			a.PresentationPartTotal = 2
			a.PresentationGroupKey = "book"
			a.PresentationKind = "multipart"
			f.files[0].PresentationPartIndex = 1
			f.files = append(f.files, &a)
		}},
		{"missing sibling", func(f *manifestFixture) {
			a := *f.files[0]
			a.ID = 8
			a.MissingSince = new(time.Now())
			f.files = append(f.files, &a)
		}},
		{"ambiguous editions", func(f *manifestFixture) {
			a := *f.files[0]
			a.ID = 8
			a.EditionKey = "other"
			f.files = append(f.files, &a)
		}},
		{"oversized", func(f *manifestFixture) {
			for range clientPlaybackManifestLimit {
				a := *f.files[0]
				a.ID = len(f.files) + 7
				f.files = append(f.files, &a)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &manifestFixture{files: []*models.MediaFile{manifestTestFile()}, items: []*models.MediaItem{{ContentID: "book", Type: "audiobook"}}}
			tc.change(f)
			got, err := resolveManifestTest(t, f, manifestTestScope())
			if err == nil || got.TimelineID != "" {
				t.Fatalf("unsafe manifest accepted: %+v %v", got, err)
			}
		})
	}
}
func TestClientPlaybackManifestRequiresScopeAndDependencies(t *testing.T) {
	resolver := &ClientPlaybackManifestResolver{}
	if _, err := resolver.ResolveClientPlaybackManifest(t.Context(), 3, "viewer", 7); !errors.Is(err, ErrItemNotFound) {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveClientPlaybackManifest(access.SetScope(t.Context(), manifestTestScope()), 3, "viewer", 7); !errors.Is(err, playback.ErrClientPlaybackTimelineV3) {
		t.Fatal(err)
	}
}

func manifestMultipartFiles() []*models.MediaFile {
	files := make([]*models.MediaFile, 3)
	for i := range files {
		f := manifestTestFile()
		f.ID += i
		f.Duration = 10 * (i + 1)
		f.PresentationKind = "multipart"
		f.PresentationGroupKey = "book"
		f.PresentationPartIndex = i + 1
		f.PresentationPartTotal = len(files)
		files[i] = f
	}
	return files
}

func TestClientPlaybackManifestMultipart(t *testing.T) {
	files := manifestMultipartFiles()
	fixture := &manifestFixture{files: []*models.MediaFile{files[2], files[0], files[1]}, items: []*models.MediaItem{{ContentID: "book", Type: "audiobook"}}}
	got, err := resolveManifestTest(t, fixture, manifestTestScope())
	if err != nil {
		t.Fatal(err)
	}
	if got.DurationSeconds != 60 || got.Parts[1].FileID != 8 || got.Parts[1].OffsetSeconds != 10 {
		t.Fatalf("wrong catalog order: %+v", got)
	}
	other := *files[0]
	other.ID = 40
	other.PresentationGroupKey = "other-edition"
	fixture.files = append(fixture.files, &other)
	isolated, err := resolveManifestTest(t, fixture, manifestTestScope())
	if err != nil || isolated.TimelineID != got.TimelineID {
		t.Fatalf("distinct edition folded into digest: %v", err)
	}
	files[0].PresentationPartIndex, files[1].PresentationPartIndex = 2, 1
	changed, err := resolveManifestTest(t, fixture, manifestTestScope())
	if err != nil || changed.TimelineID == got.TimelineID || changed.Parts[0].FileID != 8 {
		t.Fatalf("catalog order edit not captured: %+v %v", changed, err)
	}
}

func TestClientPlaybackManifestMultipartRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]*models.MediaFile) []*models.MediaFile
	}{
		{"gap", func(f []*models.MediaFile) []*models.MediaFile { return f[:2] }},
		{"duplicate index", func(f []*models.MediaFile) []*models.MediaFile { f[1].PresentationPartIndex = 1; return f }},
		{"duplicate file", func(f []*models.MediaFile) []*models.MediaFile { f[1].ID = 7; return f }},
		{"zero index", func(f []*models.MediaFile) []*models.MediaFile { f[1].PresentationPartIndex = 0; return f }},
		{"out of range", func(f []*models.MediaFile) []*models.MediaFile { f[1].PresentationPartIndex = 4; return f }},
		{"mixed total", func(f []*models.MediaFile) []*models.MediaFile { f[1].PresentationPartTotal = 4; return f }},
		{"mixed edition", func(f []*models.MediaFile) []*models.MediaFile { f[1].EditionKey = "different"; return f }},
		{"mixed kind", func(f []*models.MediaFile) []*models.MediaFile { f[1].PresentationKind = "single"; return f }},
		{"ungrouped", func(f []*models.MediaFile) []*models.MediaFile { f[1].PresentationGroupKey = ""; return f }},
		{"missing", func(f []*models.MediaFile) []*models.MediaFile { f[1].MissingSince = new(time.Now()); return f }},
		{"no duration", func(f []*models.MediaFile) []*models.MediaFile { f[1].Duration = 0; return f }},
		{"unprobed", func(f []*models.MediaFile) []*models.MediaFile { f[1].ProbeSource = ""; return f }},
		{"unauthorized part", func(f []*models.MediaFile) []*models.MediaFile { f[1].MediaFolderID = 99; return f }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &manifestFixture{files: tc.change(manifestMultipartFiles()), items: []*models.MediaItem{{ContentID: "book", Type: "audiobook"}}}
			scope := manifestTestScope()
			scope.AllowedLibraryIDs = []int{2}
			got, err := resolveManifestTest(t, f, scope)
			if err == nil || got.TimelineID != "" {
				t.Fatalf("accepted invalid group: %+v", got)
			}
		})
	}
}

func TestClientPlaybackManifestMultipartBound(t *testing.T) {
	files := make([]*models.MediaFile, clientPlaybackManifestLimit)
	for i := range files {
		f := manifestTestFile()
		f.ID = 7 + i
		f.Duration = 1
		f.PresentationKind = "multipart"
		f.PresentationGroupKey = "catalog-group"
		f.PresentationPartIndex = i + 1
		f.PresentationPartTotal = len(files)
		files[i] = f
	}
	fixture := &manifestFixture{files: files, items: []*models.MediaItem{{ContentID: "book", Type: "audiobook"}}}
	got, err := resolveManifestTest(t, fixture, manifestTestScope())
	if err != nil || len(got.Parts) != 4096 || got.DurationSeconds != 4096 {
		t.Fatalf("valid bound refused: %v", err)
	}
	files[0].PresentationPartTotal++
	if _, err := resolveManifestTest(t, fixture, manifestTestScope()); err == nil {
		t.Fatal("overflow total accepted")
	}
}
