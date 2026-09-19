package executor

import (
	"encoding/json"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// Device mutation follow-ups inspect populated overrides on the surviving
// devices. Keep that prerequisite local to removal rows: the default device-list
// fixtures deliberately have no overrides. Install before the row's first
// Reseed so paired transports and FreshState requests receive the same state.
func (e *Env) withBloemDeviceRowFixture(row scenariocatalog.Row) func() {
	if !e.HasDatabase() || row.Listener != "api" || row.Method != http.MethodDelete ||
		(row.Path != "/api/v1/devices/{device_id}" && row.Path != "/api/v1/devices/{device_id}/settings") {
		return func() {}
	}
	previous := e.afterReseed
	e.afterReseed = func() {
		if previous != nil {
			previous()
		}
		store, err := e.stores.ForUser(e.ctx, e.users[fixtureMember].ID)
		if err != nil {
			e.t.Fatalf("scenario executor: device fixture store: %v", err)
		}
		for _, target := range []struct{ profile, device string }{
			{profilePrimary, deviceIDA},
			{profilePrimary, deviceIDB},
			{profileSecondary, "fixture-device-c"},
		} {
			// Canonical writes leave the registry's identity, metadata and
			// deterministic last-seen ordering intact. SetDeviceSetting would
			// re-register the device and change those frozen prerequisites.
			if _, err := store.UpsertSettingValue(e.ctx, userstore.SettingIdentity{
				Key: "theme", Scope: settingscontract.ScopeProfileDevice,
				ProfileID: target.profile, DeviceID: target.device,
			}, json.RawMessage(`"dark"`)); err != nil {
				e.t.Fatalf("scenario executor: device fixture override: %v", err)
			}
		}
	}
	return func() {
		e.afterReseed = previous
		e.Reseed()
	}
}
