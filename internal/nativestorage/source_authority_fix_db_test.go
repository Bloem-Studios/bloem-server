//go:build integration

package nativestorage

import (
	"context"
	"errors"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestNativeOnboardingDetachedInstallationAssociationDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	svc, first, cmd := nativeDomainSourceFixture(t, pool, actor)
	second, err := svc.Install(t.Context(), actor, cmd)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &runtimeStub{}
	svc.SetCoordinator(&Coordinator{Runtime: runtime})
	for _, source := range []SourceView{first, second} {
		if err = svc.Remove(t.Context(), actor, int(*source.InstallationID), source.SourceKey, 1, false); err != nil {
			t.Fatal(err)
		}
	}
	before := nativeDomainLogicalState(t, pool)
	canceled := runtime.disabled
	// Real same-owner/plugin wrong tuple must fail before any runtime fence.
	err = svc.Remove(t.Context(), actor, int(*second.InstallationID), first.SourceKey, 2, true)
	var typed *catalog.NativeOnboardingError
	if !errors.As(err, &typed) || typed.Code != "not_found" {
		t.Errorf("cross-paired detached uninstall admitted unrelated S fence: %v", err)
	}
	if runtime.disabled != canceled {
		t.Error("cross-paired detached preflight reached runtime cancellation")
	}
	if before != nativeDomainLogicalState(t, pool) {
		t.Fatal("cross-paired detached uninstall changed whole logical rows / source revisions")
	}
	// Independently exercise registry association enforcement: actual actor and
	// detached-source mutation authority is retained, but no installation tuple is
	// manufactured by the callback. Registry must reject the wrong selected ID.
	owner := nativeTestSourceOwner(t, svc, first.SourceKey)
	source, err := storagesource.NewRepository(pool).Source(t.Context(), first.SourceKey)
	if err != nil {
		t.Fatal(err)
	}
	actualAuthorize := svc.sourceMutationAuthorizer(actor, source, nil, 2)
	err = svc.registry.RemoveAuthorized(t.Context(), int(*second.InstallationID), first.SourceKey, owner, 2, true, actualAuthorize)
	if !errors.Is(err, storagesource.ErrSourceUnavailable) {
		t.Errorf("registry cross-pair must refuse real detached-source authority: %v", err)
	}
	if before != nativeDomainLogicalState(t, pool) {
		t.Fatal("registry cross-pair changed logical rows")
	}
	if err = svc.Remove(t.Context(), actor, int(*first.InstallationID), first.SourceKey, 2, true); err != nil {
		t.Fatal("correctly paired detached uninstall refused", err)
	}
	nativeDomainRequireCode(t, svc.Remove(t.Context(), actor, int(*first.InstallationID), first.SourceKey, 3, true), "not_found")
	if err = svc.Remove(t.Context(), actor, int(*second.InstallationID), second.SourceKey, 2, true); err != nil {
		t.Fatal(err)
	}
}

func nativeLineage(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, key uuid.UUID) *int64 {
	t.Helper()
	var lineage *int64
	if err := q.QueryRow(t.Context(), "SELECT latest_installation_id FROM bloem_storage_sources WHERE key=$1", key).Scan(&lineage); err != nil {
		t.Fatal(err)
	}
	return lineage
}

func TestNativeOnboardingSourceLineageLifecycleDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	svc, source, cmd := nativeDomainSourceFixture(t, pool, actor)
	runtime := &runtimeStub{}
	svc.SetCoordinator(&Coordinator{Runtime: runtime})
	original := *source.InstallationID
	if lineage := nativeLineage(t, pool, source.SourceKey); lineage == nil || *lineage != original {
		t.Fatal("actual Install omitted latest marked bigint witness")
	}
	owner := nativeTestSourceOwner(t, svc, source.SourceKey)
	repo := storagesource.NewRepository(pool)
	sibling, err := repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: owner, InstallationID: &original, PluginID: source.PluginID, ProviderSourceID: "lineage-sibling", RootEntryID: "sibling-root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ReplaceConfiguration(t.Context(), actor, source.SourceKey, 1, cmd.Config); err != nil {
		t.Fatal(err)
	}
	if lineage := nativeLineage(t, pool, source.SourceKey); lineage == nil || *lineage != original {
		t.Fatal("config changed latest association")
	}
	if err = svc.Remove(t.Context(), actor, int(original), source.SourceKey, 2, false); err != nil {
		t.Fatal(err)
	}
	for _, key := range []uuid.UUID{source.SourceKey, sibling.Key} {
		if lineage := nativeLineage(t, pool, key); lineage == nil || *lineage != original {
			t.Fatal("real detachment lost attached sibling association")
		}
	}
	// A genuinely unknown historical detached row remains refused before Fence.
	if _, err = pool.Exec(t.Context(), "UPDATE bloem_storage_sources SET latest_installation_id=NULL WHERE key=$1", sibling.Key); err != nil {
		t.Fatal(err)
	}
	before := nativeDomainLogicalState(t, pool)
	canceled := runtime.disabled
	nativeDomainRequireCode(t, svc.Remove(t.Context(), actor, int(original), sibling.Key, 3, true), "not_found")
	if before != nativeDomainLogicalState(t, pool) || runtime.disabled != canceled {
		t.Fatal("unknown detached lineage reached fence or changed rows")
	}
	if err = svc.Remove(t.Context(), actor, int(original), source.SourceKey, 3, true); err != nil {
		t.Fatal(err)
	}
	current := int64(4)
	cmd.SourceKey = &source.SourceKey
	cmd.ExpectedRevision = &current
	reattached, err := svc.Install(t.Context(), actor, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if reattached.SourceKey != source.SourceKey || reattached.ConfigurationRevision != 5 || *reattached.InstallationID == original {
		t.Fatal("same-key empty namespace reattach changed identity/revision")
	}
	if lineage := nativeLineage(t, pool, source.SourceKey); lineage == nil || *lineage != *reattached.InstallationID {
		t.Fatal("reattach failed to replace latest association")
	}
	// Populate via actual discovery, then uninstall and reject namespace reattach.
	lease, err := repo.Begin(t.Context(), source.SourceKey, "lineage-retained", nativeLineageLease)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, ok, err := repo.NextDirectory(t.Context(), lease)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err = repo.ApplyPage(t.Context(), lease, checkpoint, nativeLineagePage()); err != nil {
		t.Fatal(err)
	}
	if err = svc.Remove(t.Context(), actor, int(*reattached.InstallationID), source.SourceKey, 5, true); err != nil {
		t.Fatal(err)
	}
	current = 6
	before = nativeDomainLogicalState(t, pool)
	_, err = svc.Install(t.Context(), actor, cmd)
	nativeDomainRequireCode(t, err, "retained_namespace_unverified")
	if before != nativeDomainLogicalState(t, pool) {
		t.Fatal("populated namespace refusal changed latest/source/catalog rows")
	}
}

const nativeLineageLease = time.Minute

func nativeLineagePage() *storagev1.ListResponse {
	return &storagev1.ListResponse{Complete: true, Entries: []*storagev1.Entry{{Id: "lineage-entry", Name: "book.epub", LogicalPath: "book.epub", Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 4, Revision: "v1"}}}
}
