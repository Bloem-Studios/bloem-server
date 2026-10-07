package nativestorage

import (
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NativeStorageReady is a read-only check of the actual local host wiring.
func (h *Host) NativeStorageReady(pool *pgxpool.Pool) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	return !closed && h.Registry.NativeStorageUsesPool(pool) && h.Manager.Available()
}
func (s *LibraryManagement) NativeStorageComposition(pool *pgxpool.Pool, folders *catalog.FolderRepository, queue NativeLibraryQueue) bool {
	return s != nil && pool != nil && folders != nil && queue != nil && s.pool == pool && s.folders == folders && folders.Pool() == pool && s.queue == queue && s.sections.NativeStorageUsesPool(pool) && s.resources.NativeStorageUsesPool(pool)
}
func (s *SourceManagement) NativeStorageComposition(pool *pgxpool.Pool, host *Host, coordinator *Coordinator) bool {
	if s == nil || host == nil || coordinator == nil || s.pool != pool || s.registry != host.Registry || s.coordinator != coordinator || !host.NativeStorageReady(pool) {
		return false
	}
	refs, ok := coordinator.References.(*storagesource.Repository)
	runtime, managed := coordinator.Runtime.(ManagedRuntime)
	return ok && refs.NativeStorageUsesPool(pool) && managed && runtime.Manager == host.Manager && coordinator.Registry == host.Registry && coordinator.AcquireOpen != nil && coordinator.AuthorizeFile != nil && coordinator.AuthorizeSource != nil
}
