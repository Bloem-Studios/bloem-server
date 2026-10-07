package api

import (
	"context"
	"strings"

	"github.com/Silo-Server/silo-server/internal/adminjob"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/metadata/translation"
	"github.com/Silo-Server/silo-server/internal/models"
)

// Embedding forwards only reads, status and ordinary CreateLibrary. Every
// finite mutation below is explicit; the final wiring uses these exact bases.
type nativeMutationServices struct {
	apiv2.LibraryAdminService
	apiv2.ScanControlService
	apiv2.AdminItemMetadataService
	apiv2.AdminCatalogMatchService
	apiv2.AdminCatalogSplitService
	apiv2.AdminCatalogImagesService
	apiv2.AdminMetadataTranslationService
	apiv2.MetadataAIService
	apiv2.CatalogTrailerService
	guard *nativeMutationGuard
	admit func(context.Context, string, catalog.NativePhaseTargets, string) (context.Context, error)
}

func (s *nativeMutationServices) UpdateLibrary(ctx context.Context, id, userID int, req handlers.LibraryUpdateRequest) (handlers.LibraryView, error) {
	targets, err := s.guard.libraryTargets(ctx, id, false)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return handlers.LibraryView{}, err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return handlers.LibraryView{}, err
	}
	result, err := s.LibraryAdminService.UpdateLibrary(admitted, id, userID, req)
	if catalog.IsNativePhaseRefusal(err) {
		return handlers.LibraryView{}, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) DeleteLibrary(ctx context.Context, id, userID int) (*models.AdminJob, error) {
	targets, err := s.guard.libraryTargets(ctx, id, true)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return nil, err
	}
	admitted, err := s.admit(ctx, "admin", targets, "native_library_delete_unsupported")
	if err != nil {
		return nil, err
	}
	result, err := s.LibraryAdminService.DeleteLibrary(admitted, id, userID)
	if catalog.IsNativePhaseRefusal(err) {
		return nil, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) CheckLibraryMount(ctx context.Context, id int) (handlers.LibraryMountCheckView, error) {
	targets, err := s.guard.libraryTargets(ctx, id, false)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return handlers.LibraryMountCheckView{}, err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return handlers.LibraryMountCheckView{}, err
	}
	result, err := s.LibraryAdminService.CheckLibraryMount(admitted, id)
	if catalog.IsNativePhaseRefusal(err) {
		return handlers.LibraryMountCheckView{}, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) ConfirmEmptyRootCleanup(ctx context.Context, id int) error {
	targets, err := s.guard.libraryTargets(ctx, id, false)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return err
	}
	err = s.LibraryAdminService.ConfirmEmptyRootCleanup(admitted, id)
	if catalog.IsNativePhaseRefusal(err) {
		return s.guard.transport(ctx, err)
	}
	return err
}

func (s *nativeMutationServices) RetryMetadataMatchQueue(ctx context.Context, id int) (handlers.MetadataMatchQueueActionView, error) {
	targets, err := s.guard.libraryTargets(ctx, id, true)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return handlers.MetadataMatchQueueActionView{}, err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return handlers.MetadataMatchQueueActionView{}, err
	}
	result, err := s.LibraryAdminService.RetryMetadataMatchQueue(admitted, id)
	if catalog.IsNativePhaseRefusal(err) {
		return handlers.MetadataMatchQueueActionView{}, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) CancelMetadataMatchQueue(ctx context.Context, id int) (handlers.MetadataMatchQueueActionView, error) {
	targets, err := s.guard.libraryTargets(ctx, id, true)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return handlers.MetadataMatchQueueActionView{}, err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return handlers.MetadataMatchQueueActionView{}, err
	}
	result, err := s.LibraryAdminService.CancelMetadataMatchQueue(admitted, id)
	if catalog.IsNativePhaseRefusal(err) {
		return handlers.MetadataMatchQueueActionView{}, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) RefreshLibraryMetadata(ctx context.Context, id, userID int, mode adminjob.LibraryRefreshMode) (*models.AdminJob, error) {
	targets, err := s.guard.libraryTargets(ctx, id, true)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return nil, err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return nil, err
	}
	result, err := s.LibraryAdminService.RefreshLibraryMetadata(admitted, id, userID, mode)
	if catalog.IsNativePhaseRefusal(err) {
		return nil, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) UploadLibraryPoster(ctx context.Context, id int, contentType string, data []byte) (handlers.LibraryView, error) {
	targets, err := s.guard.libraryTargets(ctx, id, false)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return handlers.LibraryView{}, err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return handlers.LibraryView{}, err
	}
	result, err := s.LibraryAdminService.UploadLibraryPoster(admitted, id, contentType, data)
	if catalog.IsNativePhaseRefusal(err) {
		return handlers.LibraryView{}, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) DeleteLibraryPoster(ctx context.Context, id int) error {
	targets, err := s.guard.libraryTargets(ctx, id, false)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return err
	}
	err = s.LibraryAdminService.DeleteLibraryPoster(admitted, id)
	if catalog.IsNativePhaseRefusal(err) {
		return s.guard.transport(ctx, err)
	}
	return err
}

func (s *nativeMutationServices) SetLibraryProviders(ctx context.Context, id int, levels map[string][]handlers.ProviderChainEntryInput) error {
	targets, err := s.guard.libraryTargets(ctx, id, false)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return err
	}
	err = s.LibraryAdminService.SetLibraryProviders(admitted, id, levels)
	if catalog.IsNativePhaseRefusal(err) {
		return s.guard.transport(ctx, err)
	}
	return err
}

func (s *nativeMutationServices) ReorderLibraries(ctx context.Context, entries []catalog.FolderReorderEntry) error {
	targets, err := nativeReorderTargets(entries)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return err
	}
	err = s.LibraryAdminService.ReorderLibraries(admitted, entries)
	if catalog.IsNativePhaseRefusal(err) {
		return s.guard.transport(ctx, err)
	}
	return err
}

func (s *nativeMutationServices) SetRootOverride(ctx context.Context, userID int, req handlers.RootOverrideUpsertRequest) error {
	targets, err := s.guard.rootTargets(ctx, req.LibraryID, req.RootPath)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return err
	}
	err = s.LibraryAdminService.SetRootOverride(admitted, userID, req)
	if catalog.IsNativePhaseRefusal(err) {
		return s.guard.transport(ctx, err)
	}
	return err
}

func (s *nativeMutationServices) DeleteRootOverride(ctx context.Context, req handlers.RootOverrideDeleteRequest) error {
	targets, err := s.guard.rootTargets(ctx, req.LibraryID, req.RootPath)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return err
	}
	err = s.LibraryAdminService.DeleteRootOverride(admitted, req)
	if catalog.IsNativePhaseRefusal(err) {
		return s.guard.transport(ctx, err)
	}
	return err
}

func (s *nativeMutationServices) RematchStaleID(ctx context.Context, id string) error {
	targets, err := nativeItemTarget(id)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeRepairUnsupportedCode)
	if err != nil {
		return err
	}
	err = s.LibraryAdminService.RematchStaleID(admitted, id)
	if catalog.IsNativePhaseRefusal(err) {
		return s.guard.transport(ctx, err)
	}
	return err
}

func (s *nativeMutationServices) StartLibraryScan(ctx context.Context, id *int, path string) (handlers.ScanAdmission, error) {
	targets, err := s.guard.scanTargets(ctx, id, path)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return handlers.ScanAdmission{}, err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return handlers.ScanAdmission{}, err
	}
	result, err := s.ScanControlService.StartLibraryScan(admitted, id, path)
	if catalog.IsNativePhaseRefusal(err) {
		return handlers.ScanAdmission{}, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) CancelLibraryScans(ctx context.Context, id int) (handlers.ScanCancellation, error) {
	targets, err := s.guard.libraryTargets(ctx, id, true)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return handlers.ScanCancellation{}, err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return handlers.ScanCancellation{}, err
	}
	result, err := s.ScanControlService.CancelLibraryScans(admitted, id)
	if catalog.IsNativePhaseRefusal(err) {
		return handlers.ScanCancellation{}, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) CreateItemMetadataRefresh(ctx context.Context, id string, mode adminjob.ItemRefreshMode, userID int) (*models.AdminJob, error) {
	targets, err := nativeItemTarget(id)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return nil, err
	}
	admitted, err := s.admit(ctx, "curation", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return nil, err
	}
	result, err := s.AdminItemMetadataService.CreateItemMetadataRefresh(admitted, id, mode, userID)
	if catalog.IsNativePhaseRefusal(err) {
		return nil, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) UpdateCatalogItemMetadata(ctx context.Context, id string, req handlers.UpdateItemMetadataRequest) (*catalog.ItemDetail, error) {
	targets, err := nativeItemTarget(id)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return nil, err
	}
	admitted, err := s.admit(ctx, "curation", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return nil, err
	}
	result, err := s.AdminItemMetadataService.UpdateCatalogItemMetadata(admitted, id, req)
	if catalog.IsNativePhaseRefusal(err) {
		return nil, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) SearchAdminItemMatches(ctx context.Context, id string, req handlers.AdminMatchSearchRequest) (handlers.AdminMatchSearchResult, error) {
	targets, err := nativeMatchTargets(id, req.LibraryID)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return handlers.AdminMatchSearchResult{}, err
	}
	admitted, err := s.admit(ctx, "curation", targets, nativeRepairUnsupportedCode)
	if err != nil {
		return handlers.AdminMatchSearchResult{}, err
	}
	result, err := s.AdminCatalogMatchService.SearchAdminItemMatches(admitted, id, req)
	if catalog.IsNativePhaseRefusal(err) {
		return handlers.AdminMatchSearchResult{}, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) ApplyAdminItemMatch(ctx context.Context, id string, req handlers.AdminMatchApplyRequest) (handlers.AdminMatchApplyResult, error) {
	targets, err := nativeMatchTargets(id, req.LibraryID)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return handlers.AdminMatchApplyResult{}, err
	}
	admitted, err := s.admit(ctx, "curation", targets, nativeRepairUnsupportedCode)
	if err != nil {
		return handlers.AdminMatchApplyResult{}, err
	}
	result, err := s.AdminCatalogMatchService.ApplyAdminItemMatch(admitted, id, req)
	if catalog.IsNativePhaseRefusal(err) {
		return handlers.AdminMatchApplyResult{}, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) SplitAdminItem(ctx context.Context, id string, req handlers.AdminSplitRequest) (handlers.AdminSplitResult, error) {
	targets, err := nativeSplitTargets(id, req)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return handlers.AdminSplitResult{}, err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeRepairUnsupportedCode)
	if err != nil {
		return handlers.AdminSplitResult{}, err
	}
	result, err := s.AdminCatalogSplitService.SplitAdminItem(admitted, id, req)
	if catalog.IsNativePhaseRefusal(err) {
		return handlers.AdminSplitResult{}, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) MergeAdminItem(ctx context.Context, from, into string) (string, error) {
	targets, err := nativeMergeTargets(from, into)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return "", err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeRepairUnsupportedCode)
	if err != nil {
		return "", err
	}
	result, err := s.AdminCatalogSplitService.MergeAdminItem(admitted, from, into)
	if catalog.IsNativePhaseRefusal(err) {
		return "", s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) ApplyAdminItemImage(ctx context.Context, id string, req handlers.AdminItemImageRequest) (handlers.AdminItemImageResult, error) {
	targets, err := nativeItemTarget(id)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return handlers.AdminItemImageResult{}, err
	}
	admitted, err := s.admit(ctx, "admin", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return handlers.AdminItemImageResult{}, err
	}
	result, err := s.AdminCatalogImagesService.ApplyAdminItemImage(admitted, id, req)
	if catalog.IsNativePhaseRefusal(err) {
		return handlers.AdminItemImageResult{}, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) TranslateAdminMetadata(ctx context.Context, id string, req handlers.TranslateMetadataRequest, userID int) (*translation.Job, error) {
	targets, err := s.guard.itemTargets(ctx, id, req.IncludeChildren == nil || *req.IncludeChildren)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return nil, err
	}
	admitted, err := s.admit(ctx, "curation", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return nil, err
	}
	result, err := s.AdminMetadataTranslationService.TranslateAdminMetadata(admitted, id, req, userID)
	if catalog.IsNativePhaseRefusal(err) {
		return nil, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) CancelAdminMetadataTranslation(ctx context.Context, id string, jobID int64) error {
	targets, err := s.guard.cancelTargets(ctx, id, jobID)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return err
	}
	admitted, err := s.admit(ctx, "curation", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return err
	}
	err = s.AdminMetadataTranslationService.CancelAdminMetadataTranslation(admitted, id, jobID)
	if catalog.IsNativePhaseRefusal(err) {
		return s.guard.transport(ctx, err)
	}
	return err
}

func (s *nativeMutationServices) TranslateOnView(ctx context.Context, filter catalog.AccessFilter, id, language string, userID *int) (*translation.Job, error) {
	targets, err := nativeItemTarget(id)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return nil, err
	}
	admitted, err := s.admit(ctx, "viewer", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return nil, err
	}
	result, err := s.MetadataAIService.TranslateOnView(admitted, filter, id, language, userID)
	if catalog.IsNativePhaseRefusal(err) {
		return nil, s.guard.transport(ctx, err)
	}
	return result, err
}

func (s *nativeMutationServices) RequestTrailersRefresh(ctx context.Context, userID int, id string, resolve func() (catalog.AccessFilter, error)) (handlers.TrailerRefreshView, error) {
	targets, err := nativeItemTarget(id)
	if err != nil {
		err = s.guard.transport(ctx, err)
		return handlers.TrailerRefreshView{}, err
	}
	admitted, err := s.admit(ctx, "viewer", targets, nativeLocalOperationUnsupportedCode)
	if err != nil {
		return handlers.TrailerRefreshView{}, err
	}
	result, err := s.CatalogTrailerService.RequestTrailersRefresh(admitted, userID, id, resolve)
	if catalog.IsNativePhaseRefusal(err) {
		return handlers.TrailerRefreshView{}, s.guard.transport(ctx, err)
	}
	return result, err
}

func nativeItemTarget(id string) (catalog.NativePhaseTargets, error) {
	return catalog.NativePhaseTargets{ContentIDs: []string{id}}, nil
}
func nativeMergeTargets(from, into string) (catalog.NativePhaseTargets, error) {
	return catalog.NativePhaseTargets{ContentIDs: []string{from, strings.TrimSpace(into)}}, nil
}
func nativeMatchTargets(id string, library *int) (catalog.NativePhaseTargets, error) {
	t := catalog.NativePhaseTargets{ContentIDs: []string{id}}
	if library != nil {
		t.LibraryIDs = []int{*library}
	}
	return t, nil
}
func nativeSplitTargets(id string, req handlers.AdminSplitRequest) (catalog.NativePhaseTargets, error) {
	t := catalog.NativePhaseTargets{ContentIDs: []string{id}, FileIDs: req.FileIDs}
	if into := strings.TrimSpace(req.Target.ContentID); into != "" {
		t.ContentIDs = append(t.ContentIDs, into)
	}
	return t, nil
}
func nativeReorderTargets(entries []catalog.FolderReorderEntry) (catalog.NativePhaseTargets, error) {
	t := catalog.NativePhaseTargets{}
	for _, entry := range entries {
		t.LibraryIDs = append(t.LibraryIDs, entry.ID)
	}
	return t, nil
}

func (g *nativeMutationGuard) wrapV2(deps *apiv2.Dependencies) {
	defer g.capabilities.attach(g, deps)
	s := &nativeMutationServices{guard: g, admit: g.admission,
		LibraryAdminService: deps.LibraryAdmin, ScanControlService: deps.ScanControls,
		AdminItemMetadataService: deps.AdminItemMetadata, AdminCatalogMatchService: deps.AdminCatalogMatch,
		AdminCatalogSplitService: deps.AdminCatalogSplit, AdminCatalogImagesService: deps.AdminCatalogImages,
		AdminMetadataTranslationService: deps.AdminMetadataTranslation, MetadataAIService: deps.MetadataAI,
		CatalogTrailerService: deps.CatalogTrailers}
	if deps.LibraryAdmin != nil {
		deps.LibraryAdmin = s
	}
	if deps.ScanControls != nil {
		deps.ScanControls = s
	}
	if deps.AdminItemMetadata != nil {
		deps.AdminItemMetadata = s
	}
	if deps.AdminCatalogMatch != nil {
		deps.AdminCatalogMatch = s
	}
	if deps.AdminCatalogSplit != nil {
		deps.AdminCatalogSplit = s
	}
	if deps.AdminCatalogImages != nil {
		deps.AdminCatalogImages = s
	}
	if deps.AdminMetadataTranslation != nil {
		deps.AdminMetadataTranslation = s
	}
	if deps.MetadataAI != nil {
		deps.MetadataAI = s
	}
	if deps.CatalogTrailers != nil {
		deps.CatalogTrailers = s
	}
}

var _ apiv2.LibraryAdminService = (*nativeMutationServices)(nil)
var _ apiv2.ScanControlService = (*nativeMutationServices)(nil)
var _ apiv2.AdminItemMetadataService = (*nativeMutationServices)(nil)
var _ apiv2.AdminCatalogMatchService = (*nativeMutationServices)(nil)
var _ apiv2.AdminCatalogSplitService = (*nativeMutationServices)(nil)
var _ apiv2.AdminCatalogImagesService = (*nativeMutationServices)(nil)
var _ apiv2.AdminMetadataTranslationService = (*nativeMutationServices)(nil)
var _ apiv2.MetadataAIService = (*nativeMutationServices)(nil)
var _ apiv2.CatalogTrailerService = (*nativeMutationServices)(nil)
