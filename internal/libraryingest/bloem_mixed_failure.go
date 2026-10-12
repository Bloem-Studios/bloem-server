package libraryingest

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Silo-Server/silo-server/internal/scanner"
)

// Mixed scans still need committed local changes when an early stage fails.
// Join drainers before reading their counters; standalone scans retain their
// existing result and publication behavior.
func retainMixedFilesystemFailure(result **Result, err *error, publish bool, scanned *scanner.ScanResult, started time.Time, matched, duration *atomic.Int64, drainers *sync.WaitGroup, cancel, stop context.CancelFunc) {
	if publish || *err == nil || *result != nil {
		return
	}
	cancel()
	stop()
	drainers.Wait()
	if scanned == nil {
		scanned = &scanner.ScanResult{}
	}
	if scanned.Errors == 0 {
		scanned.Errors++
	}
	*result = &Result{ScanResult: scanned, ScanDuration: time.Since(started), MatchedFiles: int(matched.Load()), MatchDuration: time.Duration(duration.Load())}
}
