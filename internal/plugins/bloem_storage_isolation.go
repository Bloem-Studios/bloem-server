package plugins

import (
	"context"
	"errors"
	"fmt"
)

var ErrNativeStorageInstallation = errors.New("native storage installation requires the owned storage runtime")

// NativeStorageIsolationRegistry is the explicit installation marker. Errors
// fail closed; native membership is never inferred from a plugin ID or capability.
type NativeStorageIsolationRegistry interface {
	NativeStorageIDs(context.Context, []int) (map[int]bool, error)
}

// IsolateNativeStorage must run immediately after construction, before preload,
// runtime reads, or lifecycle goroutines. It also closes the Service archive and
// shared Installer mutation paths. Existing installation caches are invalidated.
func (s *Service) IsolateNativeStorage(registry NativeStorageIsolationRegistry) error {
	if s == nil || s.installations == nil || registry == nil {
		return fmt.Errorf("native storage isolation requires service and registry")
	}
	wrapped := &nativeStorageServiceStore{serviceInstallationStore: s.installations, registry: registry}
	s.installations = wrapped
	if s.archiveCache != nil {
		s.archiveCache.archives = wrapped
		s.archiveCache.verified = nil
	}
	if s.installer != nil {
		s.installer.installations = &nativeStorageInstallerStore{installationStore: s.installer.installations, registry: registry}
	}
	s.InvalidateInstallationCache()
	return nil
}

func nativeStorageGuard(ctx context.Context, registry NativeStorageIsolationRegistry, id int) error {
	ids, err := registry.NativeStorageIDs(ctx, []int{id})
	if err != nil {
		return err
	}
	if ids[id] {
		return ErrNativeStorageInstallation
	}
	return nil
}
func nativeStorageFilter(ctx context.Context, registry NativeStorageIsolationRegistry, rows []*Installation, err error) ([]*Installation, error) {
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		if r != nil {
			ids = append(ids, r.ID)
		}
	}
	marked, err := registry.NativeStorageIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make([]*Installation, 0, len(rows))
	for _, r := range rows {
		if r != nil && !marked[r.ID] {
			result = append(result, r)
		}
	}
	return result, nil
}

type nativeStorageServiceStore struct {
	serviceInstallationStore
	registry NativeStorageIsolationRegistry
}

func (s *nativeStorageServiceStore) GetByID(ctx context.Context, id int) (*Installation, error) {
	if err := nativeStorageGuard(ctx, s.registry, id); err != nil {
		return nil, err
	}
	return s.serviceInstallationStore.GetByID(ctx, id)
}
func (s *nativeStorageServiceStore) List(ctx context.Context) ([]*Installation, error) {
	rows, err := s.serviceInstallationStore.List(ctx)
	return nativeStorageFilter(ctx, s.registry, rows, err)
}
func (s *nativeStorageServiceStore) ListEnabled(ctx context.Context) ([]*Installation, error) {
	rows, err := s.serviceInstallationStore.ListEnabled(ctx)
	return nativeStorageFilter(ctx, s.registry, rows, err)
}
func (s *nativeStorageServiceStore) ListByPluginID(ctx context.Context, id string) ([]*Installation, error) {
	rows, err := s.serviceInstallationStore.ListByPluginID(ctx, id)
	return nativeStorageFilter(ctx, s.registry, rows, err)
}
func (s *nativeStorageServiceStore) ListEnabledWithCapabilityTypes(ctx context.Context, types []string) ([]*Installation, error) {
	rows, err := s.serviceInstallationStore.ListEnabledWithCapabilityTypes(ctx, types)
	return nativeStorageFilter(ctx, s.registry, rows, err)
}
func (s *nativeStorageServiceStore) Update(ctx context.Context, id int, input UpdateInstallationInput) error {
	if err := nativeStorageGuard(ctx, s.registry, id); err != nil {
		return err
	}
	return s.serviceInstallationStore.Update(ctx, id, input)
}
func (s *nativeStorageServiceStore) ListCapabilities(ctx context.Context, id int) ([]*Capability, error) {
	if err := nativeStorageGuard(ctx, s.registry, id); err != nil {
		return nil, err
	}
	return s.serviceInstallationStore.ListCapabilities(ctx, id)
}
func (s *nativeStorageServiceStore) GetArchive(ctx context.Context, id int) (*InstallationArchive, error) {
	if err := nativeStorageGuard(ctx, s.registry, id); err != nil {
		return nil, err
	}
	return s.serviceInstallationStore.GetArchive(ctx, id)
}
func (s *nativeStorageServiceStore) SaveArchive(ctx context.Context, id int, manifest []byte, checksum string, data []byte) error {
	if err := nativeStorageGuard(ctx, s.registry, id); err != nil {
		return err
	}
	return s.serviceInstallationStore.SaveArchive(ctx, id, manifest, checksum, data)
}

type nativeStorageInstallerStore struct {
	installationStore
	registry NativeStorageIsolationRegistry
}

func (s *nativeStorageInstallerStore) Update(ctx context.Context, id int, input UpdateInstallationInput) error {
	if err := nativeStorageGuard(ctx, s.registry, id); err != nil {
		return err
	}
	return s.installationStore.Update(ctx, id, input)
}
func (s *nativeStorageInstallerStore) Delete(ctx context.Context, id int) error {
	if err := nativeStorageGuard(ctx, s.registry, id); err != nil {
		return err
	}
	return s.installationStore.Delete(ctx, id)
}
func (s *nativeStorageInstallerStore) SaveArchive(ctx context.Context, id int, manifest []byte, checksum string, data []byte) error {
	if err := nativeStorageGuard(ctx, s.registry, id); err != nil {
		return err
	}
	return s.installationStore.SaveArchive(ctx, id, manifest, checksum, data)
}

// IsolateNativeStorage closes the automatic updater's separate installation
// store path. Call it before Run or registering scheduled update tasks.
func (s *AutoUpdateService) IsolateNativeStorage(registry NativeStorageIsolationRegistry) error {
	if s == nil || s.installations == nil || registry == nil {
		return fmt.Errorf("native storage isolation requires updater and registry")
	}
	s.installations = &nativeStorageAutoUpdateStore{autoUpdateInstallationStore: s.installations, registry: registry}
	return nil
}

type nativeStorageAutoUpdateStore struct {
	autoUpdateInstallationStore
	registry NativeStorageIsolationRegistry
}

func (s *nativeStorageAutoUpdateStore) List(ctx context.Context) ([]*Installation, error) {
	rows, err := s.autoUpdateInstallationStore.List(ctx)
	return nativeStorageFilter(ctx, s.registry, rows, err)
}
func (s *nativeStorageAutoUpdateStore) Update(ctx context.Context, id int, input UpdateInstallationInput) error {
	if err := nativeStorageGuard(ctx, s.registry, id); err != nil {
		return err
	}
	return s.autoUpdateInstallationStore.Update(ctx, id, input)
}
func (s *nativeStorageAutoUpdateStore) Delete(ctx context.Context, id int) error {
	if err := nativeStorageGuard(ctx, s.registry, id); err != nil {
		return err
	}
	return s.autoUpdateInstallationStore.Delete(ctx, id)
}

// IsolateNativeStorage closes a standalone installer's mutations of marked
// native installations. Service setup also applies this to its shared installer.
func (s *Installer) IsolateNativeStorage(registry NativeStorageIsolationRegistry) error {
	if s == nil || s.installations == nil || registry == nil {
		return fmt.Errorf("native storage isolation requires installer and registry")
	}
	s.installations = &nativeStorageInstallerStore{installationStore: s.installations, registry: registry}
	return nil
}

// NativeStorageHiddenInstallations is an installation store for the ordinary
// plugin handlers that hides native storage installations: they have no
// ordinary plugin manifest, settings or runtime, and are managed through the
// native storage surface instead. Lookups by ID answer not found.
type NativeStorageHiddenInstallations struct {
	*InstallationStore
	registry NativeStorageIsolationRegistry
}

func NewNativeStorageHiddenInstallations(store *InstallationStore, registry NativeStorageIsolationRegistry) *NativeStorageHiddenInstallations {
	return &NativeStorageHiddenInstallations{InstallationStore: store, registry: registry}
}

func (s *NativeStorageHiddenInstallations) hidden(ctx context.Context, id int) error {
	if err := nativeStorageGuard(ctx, s.registry, id); errors.Is(err, ErrNativeStorageInstallation) {
		return ErrInstallationNotFound
	} else if err != nil {
		return err
	}
	return nil
}
func (s *NativeStorageHiddenInstallations) GetByID(ctx context.Context, id int) (*Installation, error) {
	if err := s.hidden(ctx, id); err != nil {
		return nil, err
	}
	return s.InstallationStore.GetByID(ctx, id)
}
func (s *NativeStorageHiddenInstallations) List(ctx context.Context) ([]*Installation, error) {
	rows, err := s.InstallationStore.List(ctx)
	return nativeStorageFilter(ctx, s.registry, rows, err)
}
func (s *NativeStorageHiddenInstallations) ListEnabled(ctx context.Context) ([]*Installation, error) {
	rows, err := s.InstallationStore.ListEnabled(ctx)
	return nativeStorageFilter(ctx, s.registry, rows, err)
}
func (s *NativeStorageHiddenInstallations) ListCapabilities(ctx context.Context, id int) ([]*Capability, error) {
	if err := s.hidden(ctx, id); err != nil {
		return nil, err
	}
	return s.InstallationStore.ListCapabilities(ctx, id)
}
func (s *NativeStorageHiddenInstallations) Update(ctx context.Context, id int, input UpdateInstallationInput) error {
	if err := s.hidden(ctx, id); err != nil {
		return err
	}
	return s.InstallationStore.Update(ctx, id, input)
}
func (s *NativeStorageHiddenInstallations) Delete(ctx context.Context, id int) error {
	if err := s.hidden(ctx, id); err != nil {
		return err
	}
	return s.InstallationStore.Delete(ctx, id)
}
