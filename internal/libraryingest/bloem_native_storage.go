package libraryingest

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/cache"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

// NativeIngestor operates only on folders already authorized by the host's scan
// dispatcher. Binding presence is explicit persistent state, never path syntax.
type NativeIngestor interface {
	HasNativeBinding(context.Context, int) (bool, error)
	IngestNativeFolder(context.Context, *models.MediaFolder) (*Result, error)
}

// SetNativeIngestor is startup-only, before scans are admitted.
func (e *Executor) SetNativeIngestor(native NativeIngestor) {
	if e != nil {
		e.nativeIngestor = native
	}
}
func (e *Executor) tryNativeIngest(ctx context.Context, folder *models.MediaFolder, mode scopeMode) (*Result, bool, error) {
	if e == nil || folder == nil || e.nativeIngestor == nil {
		return nil, false, nil
	}
	bound, err := e.nativeIngestor.HasNativeBinding(ctx, folder.ID)
	if err != nil {
		return nil, true, err
	}
	if !bound {
		return nil, false, nil
	}
	if mode != scopeModeLibrary {
		return nil, true, fmt.Errorf("native storage currently requires a full library scan")
	}
	kind := strings.ToLower(strings.TrimSpace(folder.Type))
	if kind != "ebook" && kind != "ebooks" {
		return nil, true, fmt.Errorf("native storage currently supports ebook libraries")
	}
	for _, root := range folder.Paths {
		if strings.TrimSpace(root) != "" {
			return nil, true, fmt.Errorf("native storage bindings cannot be mixed with filesystem roots")
		}
	}
	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	claim := scopeClaim{folderID: folder.ID, mode: scopeModeLibrary}
	entry, err := e.begin(scanCtx, claim, cancel)
	if err != nil {
		return nil, true, err
	}
	defer e.finish(entry)
	reportProgress(scanCtx, ProgressUpdate{Phase: "preparing", Message: "Preparing native storage scan"})
	result, err := e.nativeIngestor.IngestNativeFolder(scanCtx, folder)
	if err != nil {
		return result, true, err
	}
	if result == nil {
		return nil, true, fmt.Errorf("native storage ingest returned no result")
	}
	if e.folders != nil {
		if err = e.folders.UpdateLastScanned(scanCtx, folder.ID, e.now().UTC()); err != nil {
			return result, true, fmt.Errorf("update native last scanned: %w", err)
		}
	}
	if shouldPublish(result) && e.events != nil {
		if err = e.events.Publish(scanCtx, cache.ChannelCatalog, cache.Event{Type: cache.EventScanComplete, Payload: strconv.Itoa(folder.ID)}); err != nil {
			return result, true, err
		}
	}
	if shouldPublish(result) && e.realtime != nil {
		if err = e.realtime.PublishCatalogLibraryChanged(scanCtx, notifications.LibraryChangeEvent{
			LibraryID: folder.ID, Reason: "scan",
			New:     scanResultCount(result.ScanResult, func(v *scanner.ScanResult) int { return v.New }),
			Updated: scanResultCount(result.ScanResult, func(v *scanner.ScanResult) int { return v.Updated }),
			Missing: scanResultCount(result.ScanResult, func(v *scanner.ScanResult) int { return v.Missing }),
		}); err != nil {
			return result, true, err
		}
	}
	reportProgress(scanCtx, ProgressUpdate{Phase: "completed", Message: "Native storage scan completed"})
	return result, true, nil
}
