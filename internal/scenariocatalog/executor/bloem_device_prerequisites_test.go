package executor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
)

// The ordinary runner must supply the same mutation prerequisites as the
// dedicated gate, including each transport's FreshState reseeds and follow-ups.
func TestBloemDevicePrerequisitesGenericRunner(t *testing.T) {
	catalogs, err := scenariocatalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := scenariocatalog.DeviceMutationAcceptance(catalogs)
	if err != nil {
		t.Fatal(err)
	}
	e := New(t)
	if !e.HasDatabase() {
		t.Fatal(DatabaseEnv + " is required for device fixture regression coverage")
	}
	results := runAll(t, selected, e)
	if err := requiredPairedResults(results, scenariocatalog.RequiredDeviceMutationScenarios); err != nil {
		t.Error(err)
	}
	if e.afterReseed != nil {
		t.Fatal("device row fixture retained its reseed hook")
	}
	var settings, devices int
	if err := e.pool.QueryRow(e.ctx, `SELECT (SELECT count(*) FROM user_setting_values WHERE scope='profile_device'), (SELECT count(*) FROM user_devices)`).Scan(&settings, &devices); err != nil {
		t.Fatal(err)
	}
	if settings != 0 || devices != 3 {
		t.Errorf("device fixture teardown left %d overrides and %d devices, want 0 and 3", settings, devices)
	}
}

// Overlapping identifiers and both settings generations catch a removal that
// crosses profile/account boundaries or deletes inherited profile preferences.
func TestBloemDevicePrerequisitesPreserveOtherSettings(t *testing.T) {
	catalogs, err := scenariocatalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := scenariocatalog.DeviceRemovalAcceptance(catalogs)
	if err != nil {
		t.Fatal(err)
	}
	e := New(t)
	if !e.HasDatabase() {
		t.Fatal(DatabaseEnv + " is required for device fixture regression coverage")
	}
	for _, c := range selected {
		for _, row := range c.Rows {
			for _, s := range row.Scenarios {
				if !strings.HasSuffix(s.ID, ".shape") {
					continue
				}
				for _, transport := range []string{"v1", "v2"} {
					t.Run(s.ID+"/"+transport, func(t *testing.T) {
						defer e.withBloemDeviceRowFixture(row)()
						e.Reseed()
						deviceRemovalOverlay(t, e)
						before := bloemDeviceSettingsSnapshot(t, e)
						request, expect, method := s.Request, s.Expect, row.Method
						if transport == "v2" {
							request, expect, method = s.V2Expectation.Request, s.V2Expectation.Expect, s.V2Expectation.Method
						}
						req, err := e.buildRequest(e.live.URL, method, request, s.Principal)
						if err != nil {
							t.Fatal(err)
						}
						resp, err := send(req)
						if err != nil {
							t.Fatal(err)
						}
						if failures := check(expect, resp); len(failures) > 0 {
							t.Errorf("frozen exchange assertions: %v", failures)
						}
						after := bloemDeviceSettingsSnapshot(t, e)
						e.checkDeviceRemovalEffects(t, s.ID, before, after)
						for table, expected := range before {
							if !bytes.Equal(expected, after[table]) {
								t.Errorf("device removal changed non-target rows in %s", table)
							}
						}
					})
				}
			}
		}
	}
}

func bloemDeviceSettingsSnapshot(t *testing.T, e *Env) map[string]json.RawMessage {
	t.Helper()
	var raw []byte
	if err := e.pool.QueryRow(e.ctx, `SELECT jsonb_build_object(
		'devices', (SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM user_devices x),
		'canonical', (SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM user_setting_values x),
		'legacy_device_settings', (SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM user_device_settings x))`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
