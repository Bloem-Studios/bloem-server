package plugins

import (
	"context"
	"errors"
	"testing"
)

type nativeIsolationFixture struct {
	ids map[int]bool
	err error
}

func (f nativeIsolationFixture) NativeStorageIDs(_ context.Context, ids []int) (map[int]bool, error) {
	return f.ids, f.err
}

func TestNativeStorageIsolationActualServiceMethods(t *testing.T) {
	store := newFakeServiceInstallationStore(&Installation{ID: 7, PluginID: "native.fixture", Kind: KindPlugin, Enabled: true, InstallPath: "/absent-native"}, &Installation{ID: 8, PluginID: "silo.builtin", Kind: KindBuiltin, Enabled: true})
	svc := &Service{installations: store, archiveCache: NewArchiveCache(store)}
	// Populate the ordinary read cache first: setup must invalidate it.
	if _, err := svc.IsInstallationEnabled(t.Context(), 7); err != nil {
		t.Fatal(err)
	}
	if err := svc.IsolateNativeStorage(nativeIsolationFixture{ids: map[int]bool{7: true}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PreloadEnabled(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Start(t.Context(), 7); !errors.Is(err, ErrNativeStorageInstallation) {
		t.Fatalf("native Start: %v", err)
	}
	if _, err := svc.ManifestForInstallation(t.Context(), 7); !errors.Is(err, ErrNativeStorageInstallation) {
		t.Fatalf("native manifest: %v", err)
	}
	svc.configs = &fakeServiceConfigStore{}
	if err := svc.SetGlobalConfig(t.Context(), 7, "connection", map[string]any{}); !errors.Is(err, ErrNativeStorageInstallation) {
		t.Fatalf("native config: %v", err)
	}
	if _, err := svc.UpdateToAvailableVersion(t.Context(), 7); !errors.Is(err, ErrNativeStorageInstallation) {
		t.Fatalf("native update: %v", err)
	}
	if _, err := svc.archiveCache.archives.GetArchive(t.Context(), 7); !errors.Is(err, ErrNativeStorageInstallation) {
		t.Fatalf("archive bypass: %v", err)
	}
	if enabled, err := svc.IsInstallationEnabled(t.Context(), 8); err != nil || !enabled {
		t.Fatalf("ordinary builtin: %v %v", enabled, err)
	}
	for _, list := range []func(context.Context) ([]*Installation, error){svc.installations.List, svc.installations.ListEnabled, func(ctx context.Context) ([]*Installation, error) {
		return svc.installations.ListByPluginID(ctx, "native.fixture")
	}, func(ctx context.Context) ([]*Installation, error) {
		return svc.installations.ListEnabledWithCapabilityTypes(ctx, []string{"metadata_provider"})
	}} {
		rows, err := list(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID == 7 {
				t.Fatal("native leaked from generic list")
			}
		}
	}
}

func TestNativeStorageIsolationFailsClosed(t *testing.T) {
	svc := &Service{installations: newFakeServiceInstallationStore(&Installation{ID: 7, Enabled: true})}
	sentinel := errors.New("registry unavailable")
	if err := svc.IsolateNativeStorage(nativeIsolationFixture{err: sentinel}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IsInstallationEnabled(t.Context(), 7); !errors.Is(err, sentinel) {
		t.Fatalf("registry error ignored: %v", err)
	}
	if err := svc.PreloadEnabled(t.Context()); !errors.Is(err, sentinel) {
		t.Fatalf("list registry error ignored: %v", err)
	}
}

func TestNativeStorageIsolationPreservesOrdinaryServiceLaunch(t *testing.T) {
	svc, ordinary := newManifestReadService(t, 4096)
	host := svc.host.(*fakeServiceHost)
	if err := svc.IsolateNativeStorage(nativeIsolationFixture{ids: map[int]bool{99: true}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PreloadEnabled(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Start(t.Context(), ordinary.ID); err != nil {
		t.Fatal(err)
	}
	if len(host.started) != 1 || host.started[0].InstallationID != ordinary.ID {
		t.Fatalf("ordinary launch changed: %+v", host.started)
	}
}

func TestNativeStorageIsolationInstallerAndUpdaterPaths(t *testing.T) {
	registry := nativeIsolationFixture{ids: map[int]bool{7: true}}
	installer := NewInstaller(newRecordingInstallationStore(), InstallerOptions{BaseDir: t.TempDir()})
	if err := installer.IsolateNativeStorage(registry); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func() error{
		func() error { return installer.installations.Delete(t.Context(), 7) },
		func() error { return installer.installations.Update(t.Context(), 7, UpdateInstallationInput{}) },
		func() error {
			return installer.installations.SaveArchive(t.Context(), 7, []byte("{}"), "checksum", []byte("archive"))
		},
	} {
		if err := mutation(); !errors.Is(err, ErrNativeStorageInstallation) {
			t.Fatalf("installer bypass: %v", err)
		}
	}
	updater := &AutoUpdateService{installations: &fakeAutoUpdateInstallations{list: []*Installation{{ID: 7}, {ID: 8}}}}
	if err := updater.IsolateNativeStorage(registry); err != nil {
		t.Fatal(err)
	}
	rows, err := updater.installations.List(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != 8 {
		t.Fatalf("updater list: %+v %v", rows, err)
	}
	if err = updater.installations.Delete(t.Context(), 7); !errors.Is(err, ErrNativeStorageInstallation) {
		t.Fatalf("updater delete: %v", err)
	}
	if err = updater.installations.Update(t.Context(), 7, UpdateInstallationInput{}); !errors.Is(err, ErrNativeStorageInstallation) {
		t.Fatalf("updater update: %v", err)
	}
}
