//go:build integration

package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// storageMovieProbeTool stands in for ffprobe by re-executing this test binary.
func storageMovieProbeTool(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ffprobe")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nGORACE=atexit_sleep_ms=0 STORAGE_MOVIE_PROBE_TOOL=abandon exec " + quote(executable) + " -test.run=^TestStorageMovieProbeToolProcess$ -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestStorageMovieProbeToolProcess behaves like ffprobe on a remote object: it
// reads the start of an open-ended range, and once the provider is still
// streaming the rest, prints its result and exits with that range in flight.
func TestStorageMovieProbeToolProcess(t *testing.T) {
	if os.Getenv("STORAGE_MOVIE_PROBE_TOOL") != "abandon" {
		return
	}
	request, err := http.NewRequest("GET", os.Args[len(os.Args)-1], nil)
	if err != nil {
		os.Exit(96)
	}
	request.Header.Set("Range", "bytes=0-")
	response, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(request) //nolint:bodyclose // Exits with this range in flight, as ffprobe does.
	if err != nil {
		os.Exit(97)
	}
	if _, err := io.ReadFull(response.Body, make([]byte, 16)); err != nil {
		os.Exit(98)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		if marker, err := os.ReadFile(os.Getenv("STORAGE_MOVIE_PROBE_GATE")); err == nil && string(marker) == "read" {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(99)
		}
	}
	fmt.Print(`{"format":{"filename":"fixture","format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"1"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":320,"height":240}]}`)
	os.Exit(0)
}

func storageScanRun(t *testing.T, f nativeMovieScanFixture) (id uuid.UUID, epoch int64, state string, released bool) {
	t.Helper()
	if err := f.pool.QueryRow(t.Context(), `SELECT id,lease_epoch,state,lease_until<=clock_timestamp() FROM bloem_storage_scan_runs
		WHERE source_key=$1 ORDER BY created_at DESC LIMIT 1`, f.installed.Source.Key).Scan(&id, &epoch, &state, &released); err != nil {
		t.Fatal(err)
	}
	return id, epoch, state, released
}

// ffprobe exits while an open-ended range is still streaming from the remote
// provider. Cleanup cancels that abandoned read; the scan must still publish
// the probed movie instead of ending cancelled with nothing published.
func TestNativeMovieStorageAbandonedRangeTeardownPublishes(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeMovieScanFixtureWithProbe(t, binary, manifest, true, "movies-abandon", storageMovieProbeTool(t))
	t.Setenv("STORAGE_MOVIE_PROBE_GATE", f.notify)
	result, err := f.scan.ScanStorageFolder(t.Context(), f.folder, f.location)
	if err != nil {
		t.Fatalf("teardown of an abandoned range failed the scan (canceled=%v): %v", errors.Is(err, context.Canceled), err)
	}
	if marker, err := os.ReadFile(f.notify); err != nil || string(marker) != "read" {
		t.Fatal("the probe did not leave a provider range in flight", err)
	}
	var codec string
	if err := f.pool.QueryRow(t.Context(), `SELECT codec_video FROM media_files WHERE media_folder_id=$1`, f.folder.ID).Scan(&codec); err != nil || codec != "h264" || result.ScanResult.New != 1 {
		t.Fatalf("probed movie not published: new=%d codec=%q %v", result.ScanResult.New, codec, err)
	}
	if _, _, state, _ := storageScanRun(t, f); state != "complete" {
		t.Fatalf("scan run state = %s", state)
	}
}

// A failed scan must release its claim: a retry within the lease TTL would
// otherwise fail as "storage scan lease is stale" instead of resuming.
func TestNativeMovieStorageFailureReleasesLeaseForRetry(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeMovieScanFixture(t, binary, manifest, true, "movies-late")
	_, err := f.scan.ScanStorageFolder(t.Context(), f.folder, f.location)
	if err == nil || !strings.Contains(err.Error(), `probe storage movie "Native.Movie.2024.mp4"`) {
		t.Fatalf("probe failure does not name its entry: %v", err)
	}
	run, epoch, state, released := storageScanRun(t, f)
	if state != "running" || !released {
		t.Fatalf("failed scan kept its claim: state=%s released=%v", state, released)
	}
	_, err = f.scan.ScanStorageFolder(t.Context(), f.folder, f.location)
	if err == nil || errors.Is(err, storagesource.ErrStaleLease) {
		t.Fatalf("immediate retry did not resume the journal: %v", err)
	}
	if resumed, next, _, released := storageScanRun(t, f); resumed != run || next != epoch+1 || !released {
		t.Fatalf("retry run=%s epoch=%d released=%v, want run %s epoch %d released", resumed, next, released, run, epoch+1)
	}
}

// The scan queue reports any error containing context.Canceled as an operator
// cancellation, with no message. An internal failure must be reported failed
// with an actionable message, while an operator cancel stays a cancellation
// and also releases the claim for an immediate retry.
func TestNativeMovieStorageIngestReportsFailureAndCancellation(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	t.Run("failure", func(t *testing.T) {
		f := newNativeMovieScanFixture(t, binary, manifest, true, "movies-late")
		executor := NewExecutor(nil, nil, nil, nil, nil, nil)
		executor.SetStorageIngestor(f.scan)
		_, err := executor.IngestFolder(t.Context(), f.folder)
		if err == nil || errors.Is(err, context.Canceled) {
			t.Fatalf("internal failure reported as a cancellation: %v", err)
		}
		message := err.Error()
		if !strings.HasPrefix(message, `storage scan failed: probe storage movie "Native.Movie.2024.mp4": native media probe failed (`) || strings.Contains(message, "\n") || strings.Contains(message, "http://") {
			t.Fatalf("failure message is not actionable or exposes private detail: %q", message)
		}
		if code := status.Code(err); code != codes.Unavailable {
			t.Fatalf("typed provider cause hidden from classification: %s", code)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		f := newNativeMovieScanFixture(t, binary, manifest, true, "movies-read-gate")
		executor := NewExecutor(nil, nil, nil, nil, nil, nil)
		executor.SetStorageIngestor(f.scan)
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		finished := make(chan error, 1)
		go func() { _, err := executor.IngestFolder(ctx, f.folder); finished <- err }()
		for {
			if marker, err := os.ReadFile(f.notify); err == nil && string(marker) == "read" {
				break
			}
			select {
			case err := <-finished:
				t.Fatalf("scan returned before its pinned read: %v", err)
			case <-ctx.Done():
				t.Fatal("pinned read did not start")
			case <-time.After(5 * time.Millisecond):
			}
		}
		if executor.CancelLibrary(f.folder.ID) != 1 {
			t.Fatal("running storage scan was not cancelable")
		}
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("operator cancel not reported as a cancellation: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("canceled scan did not finish")
		}
		if err := os.WriteFile(f.notify+".release", []byte("release"), 0600); err != nil {
			t.Fatal(err)
		}
		if result, err := executor.IngestFolder(ctx, f.folder); err != nil || result.ScanResult.New != 2 {
			t.Fatalf("immediate retry after cancel: %+v %v", result, err)
		}
	})
}
