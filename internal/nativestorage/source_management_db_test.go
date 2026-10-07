//go:build integration

package nativestorage

import (
	"encoding/json"
	"errors"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/plugins"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
	"time"
)

func TestNativeOnboardingSourceRevocationDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	svc, source, _ := nativeDomainSourceFixture(t, pool, actor)
	if _, err := svc.preflightMutation(t.Context(), actor, source.SourceKey, nil, source.ConfigurationRevision); err != nil {
		t.Fatal(err)
	}
	if err := auth.NewSessionRepository(pool).Revoke(t.Context(), actor.SessionID); err != nil {
		t.Fatal(err)
	}
	// RED recorded the original trusted mutation after successful outer preflight.
	// The guarded domain command must retain the originating login session.
	runtime := &runtimeStub{}
	svc.SetCoordinator(&Coordinator{Runtime: runtime})
	before := nativeDomainLogicalState(t, svc.pool)
	_, err := svc.ReplaceConfiguration(t.Context(), actor, source.SourceKey, source.ConfigurationRevision, map[string]map[string]any{"storage": {"credential": "replacement-fixture"}})
	nativeDomainRequireCode(t, err, "authorization_state_stale")
	if runtime.disabled != 0 {
		t.Fatal("revoked preflight canceled runtime")
	}
	if before != nativeDomainLogicalState(t, svc.pool) {
		t.Fatal("revoked command changed logical registry state")
	}
}

func nativeTestSourceOwner(t *testing.T, s *SourceManagement, key uuid.UUID) uuid.UUID {
	t.Helper()
	var owner uuid.UUID
	if err := s.pool.QueryRow(t.Context(), "SELECT owner_id FROM bloem_storage_sources WHERE key=$1", key).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return owner
}

func nativeDomainLogicalState(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	result := ""
	for _, table := range []string{"plugin_installations", "bloem_storage_installations", "plugin_archives", "plugin_runtime_configs", "bloem_storage_sources", "bloem_storage_bindings", "bloem_storage_entries", "bloem_storage_file_refs", "bloem_storage_scan_runs", "bloem_storage_ingestion", "bloem_native_libraries", "media_folders", "media_folder_paths", "organization_entitlements"} {
		var digest string
		query := "SELECT md5(COALESCE(string_agg(rowdata,',' ORDER BY rowdata),'')) FROM (SELECT to_jsonb(t)::text rowdata FROM " + pgx.Identifier{table}.Sanitize() + " t) rows"
		if err := pool.QueryRow(t.Context(), query).Scan(&digest); err != nil {
			t.Fatal("snapshot logical rows failed", table)
		}
		result += table + ":" + digest + ";"
	}
	return result
}

func TestNativeOnboardingSourceOwnershipLifecycleDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	platform, organization := nativeDomainActor(t, pool)
	svc, source, cmd := nativeDomainSourceFixture(t, pool, platform)
	runtime := &runtimeStub{}
	coordinator := &Coordinator{Runtime: runtime}
	svc.SetCoordinator(coordinator)
	owner := nativeTestSourceOwner(t, svc, source.SourceKey)
	// The real platform-root trigger supplies the default organization's grant.
	var entitlement uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM organization_entitlements
 WHERE organization_id=$1 AND entitlement_kind='plugin_availability'
 AND root_kind='plugin_installation' AND root_owner_id=$2
 AND plugin_installation_id=$3 AND status='active'`, organization.OrganizationID, owner, *source.InstallationID).Scan(&entitlement); err != nil {
		t.Fatal(err)
	}
	inspected, err := svc.GetSource(t.Context(), organization, source.SourceKey)
	if err != nil || inspected.SourceKey != source.SourceKey || inspected.State != "attached" || !inspected.Configured {
		t.Fatal("actual entitled organization could not inspect platform source")
	}
	data, err := json.Marshal(inspected)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"fixture-only-value", "credential", "install_path", "checksum", "manifest", "config\""} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("source metadata disclosed write-only field")
		}
	}
	before := nativeDomainLogicalState(t, pool)
	_, err = svc.ReplaceConfiguration(t.Context(), organization, source.SourceKey, 1, cmd.Config)
	nativeDomainRequireCode(t, err, "not_found")
	err = svc.Remove(t.Context(), organization, int(*source.InstallationID), source.SourceKey, 1, false)
	nativeDomainRequireCode(t, err, "not_found")
	if runtime.disabled != 0 || before != nativeDomainLogicalState(t, pool) {
		t.Fatal("subscriber mutation canceled runtime or wrote logical rows")
	}
	var nilErrors []error
	_, e := svc.registry.InstallAuthorized(t.Context(), plugins.NativeStorageInstallRequest{}, nil)
	nilErrors = append(nilErrors, e)
	_, e = svc.registry.ReplaceConfigurationAuthorized(t.Context(), source.SourceKey, owner, 1, cmd.Config, nil)
	nilErrors = append(nilErrors, e)
	nilErrors = append(nilErrors, svc.registry.RemoveAuthorized(t.Context(), int(*source.InstallationID), source.SourceKey, owner, 1, false, nil))
	for _, err := range nilErrors {
		nativeDomainRequireCode(t, err, "native_storage_unavailable")
	}
	if before != nativeDomainLogicalState(t, pool) {
		t.Fatal("nil authorizer wrote logical rows")
	}
	libs := nativeDomainLibraries(pool)
	l1, err := libs.Create(t.Context(), organization, LibraryCreateCommand{Name: "Entitled Source Books"})
	if err != nil {
		t.Fatal(err)
	}
	l2, err := libs.Initialize(t.Context(), organization, l1.LibraryID, 1)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := libs.Bind(t.Context(), organization, source.SourceKey, l2.LibraryID, 1, 2)
	if err != nil {
		t.Fatal("real platform entitlement did not permit owned-folder binding", err)
	}
	bindings, err := svc.ListBindings(t.Context(), organization, source.SourceKey, nil, 1)
	if err != nil || len(bindings.Bindings) != 1 || bindings.Bindings[0].ID != binding.BindingID {
		t.Fatal("binding inspection lost dual resource authority")
	}
	_, err = svc.ReplaceConfiguration(t.Context(), platform, source.SourceKey, 1, cmd.Config)
	{
		var typed *catalog.NativeOnboardingError
		if !errors.As(err, &typed) || typed.Code != "configuration_namespace_unverified" {
			got := ""
			if typed != nil {
				got = typed.Code
			}
			t.Errorf("actual namespace code=%s want=configuration_namespace_unverified", got)
		}
	}
	id := int(*source.InstallationID)
	if err = svc.Remove(t.Context(), platform, id, source.SourceKey, 1, false); err != nil {
		t.Fatal(err)
	}
	detached, err := svc.GetSource(t.Context(), platform, source.SourceKey)
	if err != nil || detached.State != "detached" || detached.InstallationID != nil || detached.Enabled || detached.ConfigurationRevision != 2 {
		t.Fatal("Disable did not preserve detached source/S")
	}
	_, err = svc.GetSource(t.Context(), organization, source.SourceKey)
	nativeDomainRequireCode(t, err, "not_found")
	status, err := libs.Get(t.Context(), organization, l1.LibraryID)
	if err != nil || status.State != "source_unavailable" || status.SourceKey != nil || status.SourceRevision != nil || status.ReadyToQueue {
		t.Fatal("hidden detached platform source leaked into visible library")
	}
	// Disable retained the marked installation; actual owner-matching detached
	// source authority must permit its later uninstall with current S.
	if err = svc.Remove(t.Context(), platform, id, source.SourceKey, 2, true); err != nil {
		t.Fatal("disabled installation uninstall refused", err)
	}
	uninstalled, err := svc.GetSource(t.Context(), platform, source.SourceKey)
	if err != nil || uninstalled.ConfigurationRevision != 3 || uninstalled.SourceKey != source.SourceKey {
		t.Fatal("uninstall discarded identity or failed revision fence")
	}
	err = svc.Remove(t.Context(), platform, id, source.SourceKey, 3, true)
	nativeDomainRequireCode(t, err, "not_found")
	currentS := uninstalled.ConfigurationRevision
	cmd.SourceKey = &source.SourceKey
	cmd.ExpectedRevision = &currentS
	_, err = svc.Install(t.Context(), platform, cmd)
	{
		var typed *catalog.NativeOnboardingError
		if !errors.As(err, &typed) || typed.Code != "retained_namespace_unverified" {
			got := ""
			if typed != nil {
				got = typed.Code
			}
			t.Errorf("actual namespace code=%s want=retained_namespace_unverified", got)
		}
	}
	var retainedBinding uuid.UUID
	var libraryRevision int64
	if err = pool.QueryRow(t.Context(), `SELECT b.id,n.revision FROM bloem_storage_bindings b JOIN bloem_native_libraries n ON n.folder_id=b.folder_id WHERE b.folder_id=$1`, l1.LibraryID).Scan(&retainedBinding, &libraryRevision); err != nil || retainedBinding != binding.BindingID || libraryRevision != 3 {
		t.Fatal("source lifecycle discarded binding or changed L")
	}
	if svc.coordinator != coordinator || runtime.disabled < 4 {
		t.Fatal("same Coordinator did not fence both lifecycle attempts")
	}
	var markerCount int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM bloem_storage_installations WHERE installation_id=$1", id).Scan(&markerCount); err != nil || markerCount != 0 {
		t.Fatal("uninstall retained installation marker")
	}
	// Missing reader pointer remains a readiness error even for a fresh legal
	// organization-owned installation; no alternate Coordinator is constructed.
	fresh, err := svc.Install(t.Context(), organization, InstallCommand{ArtifactKey: cmd.ArtifactKey, ProviderSourceID: cmd.ProviderSourceID, RootEntryID: cmd.RootEntryID, Enabled: true, Binary: cmd.Binary, Config: cmd.Config})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetCoordinator(nil)
	_, err = svc.ReplaceConfiguration(t.Context(), organization, fresh.SourceKey, fresh.ConfigurationRevision, cmd.Config)
	nativeDomainRequireCode(t, err, "native_storage_unavailable")
	var revision int64
	if err = pool.QueryRow(t.Context(), "SELECT configuration_revision FROM bloem_storage_sources WHERE key=$1", fresh.SourceKey).Scan(&revision); err != nil || revision != fresh.ConfigurationRevision {
		t.Fatal("missing reader pointer mutated source")
	}
	t.Log("retained platform entitlement inspection/denial, real same Coordinator fence, disable/uninstall retention and namespace refusal verified")
}

func TestNativeOnboardingSourceConfigurationCASDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	svc, source, cmd := nativeDomainSourceFixture(t, pool, actor)
	svc.SetCoordinator(&Coordinator{Runtime: &runtimeStub{}})
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := svc.ReplaceConfiguration(t.Context(), actor, source.SourceKey, 1, cmd.Config)
			results <- err
		}()
	}
	close(start)
	success := 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else {
			var typed *catalog.NativeOnboardingError
			if !errors.As(err, &typed) || typed.Code != "revision_conflict" {
				t.Fatal("expected-S loser did not observe current authorized revision", err)
			}
		}
	}
	if success != 1 {
		t.Fatal("two expected-S commands did not have exactly one winner")
	}
	var revision, generation int64
	if err := pool.QueryRow(t.Context(), `SELECT s.configuration_revision,i.runtime_generation FROM bloem_storage_sources s JOIN plugin_installations i ON i.id=s.installation_id WHERE s.key=$1`, source.SourceKey).Scan(&revision, &generation); err != nil || revision != 2 || generation != 2 {
		t.Fatal("configuration did not atomically fence S/generation")
	}
}

// The existing Coordinator invokes this barrier only after the command's real
// retained preflight commits, and before its guarded registry write begins.
type nativeSourceRevokingRuntime struct {
	*runtimeStub
	revoke func() error
	err    error
}

func (r *nativeSourceRevokingRuntime) Disable(id int) {
	r.runtimeStub.Disable(id)
	if r.revoke != nil {
		revoke := r.revoke
		r.revoke = nil
		r.err = revoke()
	}
}
func TestNativeOnboardingSourceFenceRevocationDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	svc, source, cmd := nativeDomainSourceFixture(t, pool, actor)
	runtime := &nativeSourceRevokingRuntime{runtimeStub: &runtimeStub{}, revoke: func() error { return auth.NewSessionRepository(pool).Revoke(t.Context(), actor.SessionID) }}
	coordinator := &Coordinator{Runtime: runtime}
	svc.SetCoordinator(coordinator)
	before := nativeDomainLogicalState(t, pool)
	_, err := svc.ReplaceConfiguration(t.Context(), actor, source.SourceKey, source.ConfigurationRevision, cmd.Config)
	nativeDomainRequireCode(t, err, "authorization_state_stale")
	if runtime.err != nil || runtime.disabled != 2 || svc.coordinator != coordinator {
		t.Fatal("real originating-session revocation did not run inside same Coordinator fence")
	}
	if before != nativeDomainLogicalState(t, pool) {
		t.Fatal("session revoked after preflight reached guarded write")
	}
	t.Log("actual originating-login revocation after preflight/before guarded registry write denied with unchanged logical rows")
}
func TestNativeOnboardingSourceSiblingFenceReattachDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	svc, source, cmd := nativeDomainSourceFixture(t, pool, actor)
	svc.SetCoordinator(&Coordinator{Runtime: &runtimeStub{}})
	repo := storagesource.NewRepository(pool)
	// Explicit fixture setup under the actual installation owner, using the
	// existing trusted source repository after retained command authority.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	owner := nativeTestSourceOwner(t, svc, source.SourceKey)
	if err = svc.sourceMutationAuthorizer(actor, storagesource.SourceConfig{Key: source.SourceKey, OwnerID: owner}, source.InstallationID, 1)(t.Context(), tx); err != nil {
		nativeDomainRollback(t.Context(), tx)
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	sibling, err := repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: owner, InstallationID: source.InstallationID, PluginID: source.PluginID, ProviderSourceID: "sibling-books", RootEntryID: "sibling-root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := repo.Begin(t.Context(), sibling.Key, "native-domain-sibling", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := svc.ReplaceConfiguration(t.Context(), actor, source.SourceKey, 1, cmd.Config)
	if err != nil || revision != 2 {
		t.Fatal("legal empty sibling config replacement failed", err)
	}
	var siblingS, generation, epoch int64
	var state string
	if err = pool.QueryRow(t.Context(), `SELECT s.configuration_revision,i.runtime_generation,r.lease_epoch,r.state FROM bloem_storage_sources s JOIN plugin_installations i ON i.id=s.installation_id JOIN bloem_storage_scan_runs r ON r.source_key=s.key WHERE s.key=$1`, sibling.Key).Scan(&siblingS, &generation, &epoch, &state); err != nil || siblingS != 2 || generation != 2 || epoch <= lease.Epoch || state != "failed" {
		t.Fatal("installation-wide S/generation/discovery fence absent", err)
	}
	if err = repo.Renew(t.Context(), lease, time.Minute); !errors.Is(err, storagesource.ErrStaleLease) {
		t.Fatal("old sibling discovery lease survived config replacement", err)
	}
	// Only the sibling is populated: selected-source-only checks must not pass.
	siblingLease, err := repo.Begin(t.Context(), sibling.Key, "native-domain-sibling-populated", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	siblingCheckpoint, ok, err := repo.NextDirectory(t.Context(), siblingLease)
	if err != nil || !ok {
		t.Fatal("sibling discovery checkpoint absent")
	}
	siblingPage := &storagev1.ListResponse{Complete: true, Entries: []*storagev1.Entry{{Id: "sibling-entry", Name: "book.epub", LogicalPath: "book.epub", Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 4, Revision: "v1"}}}
	if err = repo.ApplyPage(t.Context(), siblingLease, siblingCheckpoint, siblingPage); err != nil {
		t.Fatal(err)
	}
	var selectedEntries, siblingEntries, bindings int
	if err = pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM bloem_storage_entries WHERE source_key=$1),(SELECT count(*) FROM bloem_storage_entries WHERE source_key=$2),(SELECT count(*) FROM bloem_storage_bindings WHERE source_key=ANY($3::uuid[]))", source.SourceKey, sibling.Key, []uuid.UUID{source.SourceKey, sibling.Key}).Scan(&selectedEntries, &siblingEntries, &bindings); err != nil || selectedEntries != 0 || siblingEntries != 1 || bindings != 0 {
		t.Fatal("sibling-only entry fixture invalid")
	}
	siblingBefore := nativeDomainLogicalState(t, pool)
	_, err = svc.ReplaceConfiguration(t.Context(), actor, source.SourceKey, 2, cmd.Config)
	{
		var typed *catalog.NativeOnboardingError
		if !errors.As(err, &typed) || typed.Code != "configuration_namespace_unverified" {
			got := ""
			if typed != nil {
				got = typed.Code
			}
			t.Errorf("actual namespace code=%s want=configuration_namespace_unverified", got)
		}
	}
	if siblingBefore != nativeDomainLogicalState(t, pool) {
		t.Fatal("sibling-only refusal changed full state/generation/lease")
	}
	if err = svc.Remove(t.Context(), actor, int(*source.InstallationID), source.SourceKey, 2, true); err != nil {
		t.Fatal(err)
	}
	current := int64(3)
	cmd.SourceKey = &source.SourceKey
	cmd.ExpectedRevision = &current
	reattached, err := svc.Install(t.Context(), actor, cmd)
	if err != nil || reattached.SourceKey != source.SourceKey || reattached.ConfigurationRevision != 4 {
		t.Fatal("empty retained same-key reattach refused", err)
	}
	siblingRetained, err := repo.Source(t.Context(), sibling.Key)
	if err != nil || siblingRetained.InstallationID != nil || siblingRetained.Enabled || siblingRetained.ConfigurationRevision != 3 {
		t.Fatal("reattach changed sibling retained identity", err)
	}
	// Real discovery creates an entry namespace; complete replacement and retained
	// reattach must now refuse without discarding discovered evidence.
	populatedLease, err := repo.Begin(t.Context(), source.SourceKey, "native-domain-populated", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, ok, err := repo.NextDirectory(t.Context(), populatedLease)
	if err != nil || !ok {
		t.Fatal("actual discovery checkpoint absent", err)
	}
	page := &storagev1.ListResponse{Complete: true, Entries: []*storagev1.Entry{{Id: "retained-entry", Name: "book.epub", LogicalPath: "book.epub", Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 4, Revision: "v1"}}}
	if err = repo.ApplyPage(t.Context(), populatedLease, checkpoint, page); err != nil {
		t.Fatal(err)
	}
	before := nativeDomainLogicalState(t, pool)
	_, err = svc.ReplaceConfiguration(t.Context(), actor, source.SourceKey, 4, cmd.Config)
	{
		var typed *catalog.NativeOnboardingError
		if !errors.As(err, &typed) || typed.Code != "configuration_namespace_unverified" {
			got := ""
			if typed != nil {
				got = typed.Code
			}
			t.Errorf("actual namespace code=%s want=configuration_namespace_unverified", got)
		}
	}
	if before != nativeDomainLogicalState(t, pool) {
		t.Fatal("populated namespace configuration refusal changed rows")
	}
	if err = svc.Remove(t.Context(), actor, int(*reattached.InstallationID), source.SourceKey, 4, true); err != nil {
		t.Fatal(err)
	}
	current = 5
	cmd.ExpectedRevision = &current
	_, err = svc.Install(t.Context(), actor, cmd)
	{
		var typed *catalog.NativeOnboardingError
		if !errors.As(err, &typed) || typed.Code != "retained_namespace_unverified" {
			got := ""
			if typed != nil {
				got = typed.Code
			}
			t.Errorf("actual namespace code=%s want=retained_namespace_unverified", got)
		}
	}
	var retainedEntries int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM bloem_storage_entries WHERE source_key=$1", source.SourceKey).Scan(&retainedEntries); err != nil || retainedEntries != 1 {
		t.Fatal("retained namespace evidence discarded", err)
	}
	t.Log("sibling S/generation/real discovery lease fencing; empty SAME-key reattach and populated namespace refusal verified")
}
