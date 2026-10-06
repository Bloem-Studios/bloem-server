package scanner

import "context"

// ReconcileNativeEbookEnrichment repairs committed ebooks through the ordinary
// bounded durable queue path. The native caller must first authorize the actual
// persisted binding/source/library; this adapter performs no provider I/O.
func (s *Scanner) ReconcileNativeEbookEnrichment(ctx context.Context, folderID int) {
	s.reconcileMissingEbookEnrichment(ctx, folderID)
}
