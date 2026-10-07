package libraryingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/jackc/pgx/v5/pgconn"
)

type onboardingReader struct {
	row    *models.MediaFolder
	native bool
	err    error
	calls  int
	writes int
}

func (p *onboardingReader) NativeScanFolder(context.Context, int) (*models.MediaFolder, bool, error) {
	p.calls++
	return p.row, p.native, p.err
}
func (p *onboardingReader) UpdateLastScanned(context.Context, int, time.Time) error {
	p.writes++
	return nil
}

type onboardingNoReader struct{}

func (*onboardingNoReader) UpdateLastScanned(context.Context, int, time.Time) error {
	panic("bookkeeping before admission")
}

type onboardingScanSpy struct {
	Scanner
	calls     int
	modeCalls int
	modeErr   error
}

func (p *onboardingScanSpy) ScanFolder(context.Context, *models.MediaFolder) (*scanner.ScanResult, error) {
	p.calls++
	return &scanner.ScanResult{}, nil
}
func (p *onboardingScanSpy) NativeLibraryScanMode(context.Context, int) (*models.MediaFolder, bool, error) {
	p.modeCalls++
	return &models.MediaFolder{ID: 5, Enabled: true, Type: "series"}, false, p.modeErr
}

type onboardingUnknownScanner struct{ Scanner }
type onboardingNativeSpy struct {
	calls int
	bound bool
}

func (p *onboardingNativeSpy) HasNativeBinding(context.Context, int) (bool, error) {
	p.calls++
	return p.bound, nil
}
func (p *onboardingNativeSpy) IngestNativeFolder(context.Context, *models.MediaFolder) (*Result, error) {
	p.calls++
	return &Result{}, nil
}
func TestNativeOnboardingExecutorReaderSelectionAndRefusal(t *testing.T) {
	schema := &pgconn.PgError{Code: "42P01", Message: "selected missing schema"}
	var nilReader *onboardingReader
	var nilScanner *onboardingScanSpy
	tests := []struct {
		name    string
		folders FolderRepository
		scanner Scanner
		cause   error
	}{
		{"absentUnknown", nil, &onboardingUnknownScanner{}, nil},
		{"absentScanner", nil, nil, nil},
		{"typedNilScanner", nil, nilScanner, nil},
		{"typedNilFolders", nilReader, &onboardingScanSpy{}, nil},
		{"selectedNoMethod", &onboardingNoReader{}, &onboardingScanSpy{}, nil},
		{"selectedSchemaError", &onboardingReader{err: schema}, &onboardingScanSpy{}, schema},
		{"alternateReaderError", nil, &onboardingScanSpy{modeErr: schema}, schema},
		{"selectedMissingRow", &onboardingReader{}, &onboardingScanSpy{}, nil},
		{"selectedWrongID", &onboardingReader{row: &models.MediaFolder{ID: 42}}, &onboardingScanSpy{}, nil},
		{"actualOfflineScanner", nil, &scanner.Scanner{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := &retryRecordingMatcher{}
			native := &onboardingNativeSpy{bound: true}
			e := NewExecutor(tt.scanner, matcher, tt.folders, nil, nil, nil)
			e.SetNativeIngestor(native)
			progress := 0
			ctx := WithProgressReporter(t.Context(), func(ProgressUpdate) { progress++ })
			_, err := e.IngestFolder(ctx, &models.MediaFolder{ID: 5, Type: "series", Paths: []string{"/unreachable"}})
			var admission *catalog.NativeOnboardingError
			if !errors.As(err, &admission) || admission.Code != "native_storage_unavailable" {
				t.Fatalf("want unavailable, got %v", err)
			}
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Fatal("selected error lost")
			}
			if matcher.processAllCalls.Load() != 0 || matcher.retryCalls.Load() != 0 || native.calls != 0 || progress != 0 || len(e.running) != 0 {
				t.Fatal("matcher/native/progress/claim effect before admission")
			}
			if p, ok := tt.scanner.(*onboardingScanSpy); ok && p != nil {
				if p.calls != 0 {
					t.Fatal("scanner invoked")
				}
				if tt.folders != nil && p.modeCalls != 0 {
					t.Fatal("fallback to alternate after selecting folders")
				}
			}
			if p, ok := tt.folders.(*onboardingReader); ok && p != nil && p.writes != 0 {
				t.Fatal("bookkeeping invoked")
			}
		})
	}
}
func TestNativeOnboardingFixtureReadersAreFiniteLocalOnly(t *testing.T) {
	for _, s := range []alternateScanModeReader{&finalizeRecordingScanner{}, &settleStubScanner{}} {
		for _, id := range []int{-1, 0, 42, 44} {
			f, n, err := s.NativeLibraryScanMode(t.Context(), id)
			if err == nil || f != nil || n {
				t.Fatalf("fixture granted unknown/native id %d", id)
			}
		}
		f, n, err := s.NativeLibraryScanMode(t.Context(), 5)
		if err != nil || f == nil || f.ID != 5 || n {
			t.Fatal("finite local fixture unavailable")
		}
	}
}
func TestNativeOnboardingNativeCannotFallBackToLocal(t *testing.T) {
	for _, bound := range []bool{false, true} {
		spy := &onboardingScanSpy{}
		matcher := &retryRecordingMatcher{}
		native := &onboardingNativeSpy{bound: bound}
		reader := &onboardingReader{row: &models.MediaFolder{ID: 5, Type: "ebook", Enabled: true}, native: true}
		e := NewExecutor(spy, matcher, reader, nil, nil, nil)
		e.SetNativeIngestor(native)
		if !bound {
			_, err := e.IngestFolder(t.Context(), &models.MediaFolder{ID: 5, Type: "movies", Paths: []string{"/forged"}})
			var admission *catalog.NativeOnboardingError
			if !errors.As(err, &admission) || native.calls != 1 {
				t.Fatalf("disappeared binding: %v, calls %d", err, native.calls)
			}
		} else {
			_, err := e.IngestSubtree(t.Context(), &models.MediaFolder{ID: 5, Type: "movies"}, "/forged")
			var admission *catalog.NativeOnboardingError
			if !errors.As(err, &admission) || native.calls != 0 {
				t.Fatalf("partial native: %v", err)
			}
		}
		if spy.calls != 0 || matcher.processAllCalls.Load() != 0 || matcher.retryCalls.Load() != 0 {
			t.Fatal("native mode fell through to local work")
		}
	}
	// Synthetic reader returns native solely to exercise refusal branches above;
	// this is not durable mode, marker, authorization or successful publication proof.
}
