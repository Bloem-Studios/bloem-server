package scanner

import (
	"context"
	"reflect"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/models"
)

// No nil-to-local classification: this predicate proves absence of all
// catalog, blob/image and external callback capabilities in Scanner's fields.
// Typed-nil interfaces are configured and therefore excluded.
func (s *Scanner) nonPersistentParser() bool {
	return s != nil &&
		s.fileRepo == nil && s.rootSnapshotRepo == nil && s.groupSnapshotRepo == nil &&
		s.rootOverrideRepo == nil && s.groupOverrideRepo == nil && s.identityOverrideRepo == nil &&
		s.locationRepo == nil && s.groupLocationRepo == nil && s.folderRepo == nil &&
		s.libraryRepo == nil && s.episodeLibraryRepo == nil && s.itemRepo == nil &&
		s.personRepo == nil && s.episodeRepo == nil && s.extraRepo == nil &&
		s.artworkStore == nil && s.imageCacher == nil && s.markerFetcher == nil &&
		s.metadataQueue == nil && s.ebookEnrichmentQueue == nil &&
		s.movieQueueSyncer == nil && s.seriesQueueSyncer == nil && s.literaryWorkLinker == nil
}
func (s *Scanner) NativeLibraryScanMode(ctx context.Context, id int) (*models.MediaFolder, bool, error) {
	if ctx == nil || invalidCarriedRepairTx(ctx) || !s.catalogScanConfigured() || s.invalidOptionalCapabilities() || id <= 0 {
		return nil, false, &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	folder, native, err := s.folderRepo.NativeScanFolder(ctx, id)
	if err != nil {
		return nil, false, catalog.MapNativeOnboardingError(err)
	}
	if folder == nil || folder.ID != id {
		return nil, false, &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
	}
	return folder, native, nil
}
func (s *Scanner) requireLocalLibraryScan(ctx context.Context, f *models.MediaFolder) error {
	return s.requireScanAdmission(ctx, f, false, false)
}
func (s *Scanner) requireEbookLibraryScan(ctx context.Context, f *models.MediaFolder) error {
	return s.requireScanAdmission(ctx, f, true, false)
}
func (s *Scanner) requireScopedLibraryScan(ctx context.Context, f *models.MediaFolder) error {
	return s.requireScanAdmission(ctx, f, false, true)
}
func (s *Scanner) requireScanAdmission(ctx context.Context, f *models.MediaFolder, directEbook, scoped bool) error {
	unavailable := &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
	if ctx == nil || s == nil || f == nil {
		return unavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.nonPersistentParser() && repairTxFrom(ctx) == nil &&
		(directEbook || librarykind.IsEbook(f.Type) || librarykind.IsManga(f.Type) ||
			(scoped && librarykind.IsAudiobook(f.Type)) || librarykind.IsMusic(f.Type)) {
		return nil // Nonpersistent parsing only. This is not a local/native verdict.
	}
	durable, native, err := s.NativeLibraryScanMode(ctx, f.ID)
	if err != nil {
		return err
	} // Terminal: no alternate authority after selection.
	if durable == nil || durable.ID != f.ID {
		return unavailable
	}
	if native {
		return &catalog.NativeOnboardingError{Code: "native_local_operation_unsupported"}
	}
	return nil
}

// All constructor-owned repositories are required for live catalog scans.
// Catalog's owned method checks its private pools without a new shared field.
func (s *Scanner) catalogScanConfigured() bool {
	if s == nil || s.fileRepo == nil || s.rootSnapshotRepo == nil ||
		s.groupSnapshotRepo == nil || s.rootOverrideRepo == nil ||
		s.groupOverrideRepo == nil || s.identityOverrideRepo == nil ||
		s.locationRepo == nil || s.groupLocationRepo == nil || s.folderRepo == nil {
		return false
	}
	pool := s.fileRepo.Pool()
	return pool != nil &&
		s.rootSnapshotRepo.pool == pool && s.groupSnapshotRepo.pool == pool &&
		s.rootOverrideRepo.pool == pool && s.groupOverrideRepo.pool == pool &&
		s.identityOverrideRepo.pool == pool && s.locationRepo.pool == pool &&
		s.groupLocationRepo.pool == pool &&
		s.folderRepo.NativeScannerRepositoriesReady(pool, s.libraryRepo, s.episodeLibraryRepo,
			s.itemRepo, s.personRepo, s.episodeRepo, s.extraRepo)
}

func (s *Scanner) invalidOptionalCapabilities() bool {
	if s == nil {
		return true
	}
	for _, v := range []any{s.artworkStore, s.imageCacher, s.metadataQueue,
		s.ebookEnrichmentQueue, s.movieQueueSyncer, s.seriesQueueSyncer, s.literaryWorkLinker} {
		if v == nil {
			continue
		}
		r := reflect.ValueOf(v)
		switch r.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			if r.IsNil() {
				return true
			}
		}
	}
	return false
}

// A nonnil repair transaction is a live capability. A typed-nil transaction
// cannot safely carry it, even when every constructor repository is configured.
func invalidCarriedRepairTx(ctx context.Context) bool {
	tx := repairTxFrom(ctx)
	if tx == nil {
		return false
	}
	value := reflect.ValueOf(tx)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	}
	return false
}
