package libraryingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// storageRetryProgress is how long an attempt must run before its failure no
// longer counts against the next: a long scan that fails now and then has
// made progress each time and keeps going.
const storageRetryProgress = 5 * time.Minute

// SetRetry is startup-only, before scans are admitted. A storage scan that
// fails then resumes its journal where it stopped, after delay times the
// failures in a row, up to attempts failures in a row.
func (c *StorageScanner) SetRetry(attempts int, delay time.Duration) {
	if c != nil && attempts >= 0 && delay >= 0 {
		c.retries, c.retryDelay = attempts, delay
	}
}

// ScanStorageFolder scans one storage location. The scan journal keeps every
// applied page, so a failed attempt resumes rather than restarts; a provider
// or plugin hiccup costs a pause instead of the scan.
func (c *StorageScanner) ScanStorageFolder(ctx context.Context, folder *models.MediaFolder, selection storagesource.LocationSelection) (*Result, error) {
	total := &Result{ScanResult: &scanner.ScanResult{}}
	for failed := 0; ; {
		started := time.Now()
		result, err := c.scanStorageFolderOnce(ctx, folder, selection)
		mergeIngestResult(total, result)
		if err == nil || ctx.Err() != nil {
			return total, err
		}
		if time.Since(started) >= storageRetryProgress {
			failed = 0
		}
		if failed++; failed > c.retries {
			return total, err
		}
		slog.WarnContext(ctx, "storage scan failed; resuming", "component", "libraryingest", "library_id", folder.ID, "location_id", selection.Location.ID, "failures", failed, "error", err)
		timer := time.NewTimer(c.retryDelay * time.Duration(failed))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return total, err
		}
	}
}
