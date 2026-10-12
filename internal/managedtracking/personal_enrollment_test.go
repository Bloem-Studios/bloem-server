package managedtracking

import (
	"strconv"
	"testing"

	"github.com/Silo-Server/silo-server/internal/watchsync"
)

func TestPersonalEnrollmentRequiresCapablePluginAndConvergesReplay(t *testing.T) {
	s, id, p := enrollmentFixture(t)
	ctx := t.Context()
	info, err := s.GetInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	e := Enrollment{Profile: p, ProviderID: "pastime", DestinationAccountID: "10", DestinationProfileID: "20", Credential: "ptbs_synthetic_personal", Active: true, Generation: info.Generation}
	e.ID = EnrollmentID(info.InstanceID, e)
	if err = s.EnsureConnection(ctx, id, e); err != nil {
		t.Fatal(err)
	}
	account, _ := strconv.Atoi(p.AccountID)
	key := watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false)
	repo := watchsync.NewPostgresRepository(s.Pool, s.Cipher)
	read := func(want bool) {
		t.Helper()
		c, found, err := repo.GetConnection(ctx, key, account, p.ProfileID)
		if err != nil || !found {
			t.Fatal("connection", err)
		}
		if c.ImportFavoritesEnabled != want || c.ExportFavoritesEnabled != want || c.SyncFavoriteRemovalsEnabled != want || c.ImportWatchlistEnabled != want || c.ExportWatchlistEnabled != want || c.SyncWatchlistRemovalsEnabled != want || c.ImportRatingsEnabled != want || c.ExportRatingsEnabled != want {
			t.Fatal("personal lanes did not converge", want)
		}
		if want && (c.ImportWatchedEnabled || c.ImportProgressEnabled || c.ExportUnwatchedEnabled || c.SyncWatchlistOrderEnabled) {
			t.Fatal("unrelated lanes enabled")
		}
	}
	read(false)
	if _, err = s.Pool.Exec(ctx, `INSERT INTO plugin_capabilities(plugin_installation_id,capability_type,capability_id,metadata) VALUES($1,'watch_sync_provider.v1','pastime','{"metadata":{"managed_personal_state":true}}')`, id); err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureConnection(ctx, id, e); err != nil {
		t.Fatal(err)
	}
	read(true)
	if err = s.EnsureConnection(ctx, id, e); err != nil {
		t.Fatal("replay", err)
	}
	read(true)
	if _, err = s.Pool.Exec(ctx, `UPDATE plugin_capabilities SET metadata='{}' WHERE plugin_installation_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureConnection(ctx, id, e); err != nil {
		t.Fatal("capability withdrawal", err)
	}
	read(false)
}
