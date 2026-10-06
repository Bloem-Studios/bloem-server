//go:build integration

package storagesource

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestSourceRetainsOwnerAfterUninstall(t *testing.T) {
	pool := testDatabase(t, true)
	r := NewRepository(pool)
	installation := int64(91011)
	execSQL(t, pool, `INSERT INTO plugin_installations(id,plugin_id,version,install_path) VALUES(91011,'fixture','1','/synthetic')`)
	s, err := r.CreateSource(t.Context(), SourceConfig{InstallationID: &installation, PluginID: "fixture", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.OwnerID == uuid.Nil {
		t.Fatal("source has no retained owner")
	}
	execSQL(t, pool, `DELETE FROM organization_entitlements WHERE plugin_installation_id=91011;DELETE FROM plugin_installations WHERE id=91011`)
	got, err := r.Source(context.Background(), s.Key)
	if err != nil || got.OwnerID != s.OwnerID || got.InstallationID != nil {
		t.Fatalf("lost retained owner: %+v %v", got, err)
	}
}
func TestSourceRejectsMismatchedInstallationOwner(t *testing.T) {
	pool := testDatabase(t, true)
	r := NewRepository(pool)
	installation := int64(91011)
	execSQL(t, pool, `INSERT INTO plugin_installations(id,plugin_id,version,install_path) VALUES(91011,'fixture','1','/synthetic')`)
	_, err := r.CreateSource(t.Context(), SourceConfig{OwnerID: uuid.New(), InstallationID: &installation, PluginID: "fixture", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err == nil {
		t.Fatal("mismatched owner accepted")
	}
}
