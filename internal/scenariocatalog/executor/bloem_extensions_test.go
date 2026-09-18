package executor

import (
	"encoding/json"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/remote"
	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
)

// Called by the existing database-gated reseed test. No new skip path or
// alternative production authentication is introduced for these fixtures.
func checkBloemFixtureRestoration(t *testing.T, e *Env) {
	t.Helper()
	var originalSessions int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM auth_sessions`).Scan(&originalSessions); err != nil {
		t.Fatal(err)
	}
	func() {
		restore := e.withBloemRowFixture(scenariocatalog.Row{Method: "POST", Path: "/api/v1/profiles/household/sessions/{session_id}/commands"})
		defer restore()
		e.Reseed()
		claims, err := e.jwt.ValidateToken(e.fixtures["bloem_direct_token"])
		if err != nil {
			t.Fatal("fixture must mint a real direct-profile token")
		}
		if claims.AuthMethod != auth.AuthMethodDirectProfile || claims.UserID != e.users[fixtureMember].ID ||
			claims.ProfileID != profileSecondary || claims.DeviceID != bloemDirectDevice || claims.Role != "user" ||
			claims.OrganizationID != e.fixtures["bloem_organization_id"] || claims.MembershipID != e.fixtures["bloem_membership_id"] {
			t.Fatal("direct fixture confused profile, account, device or organization authority")
		}
		checkBloemStoredEffects(t, e)
		// FreshState scenarios recreate the organization. Playback fixtures
		// must follow that new authority rather than retain the previous tenant.
		e.Reseed()
		checkBloemStoredEffects(t, e)
	}()
	if e.afterReseed != nil {
		t.Fatal("extension overlay survived restoration")
	}
	if _, ok := e.fixtures["bloem_direct_token"]; ok {
		t.Fatal("extension credential survived restoration")
	}
	var directCredentials, directSessions, allSessions int
	if err := e.pool.QueryRow(e.ctx, `SELECT
		(SELECT count(*) FROM user_profiles WHERE login_email IS NOT NULL OR password_hash IS NOT NULL),
		(SELECT count(*) FROM auth_sessions WHERE auth_method = 'direct_profile'),
		(SELECT count(*) FROM auth_sessions)`).Scan(&directCredentials, &directSessions, &allSessions); err != nil {
		t.Fatal(err)
	}
	if directCredentials != 0 || directSessions != 0 || allSessions != originalSessions {
		t.Fatal("extension fixture changed the ordinary household after restoration")
	}
	var remoteRows int
	if err := e.pool.QueryRow(e.ctx, `SELECT
		(SELECT count(*) FROM remote_commands WHERE target_session_id IN ($1, $2)) +
		(SELECT count(*) FROM remote_device_capabilities)`, bloemPlaybackID, bloemForeignID).Scan(&remoteRows); err != nil {
		t.Fatal(err)
	}
	if remoteRows != 0 {
		t.Fatal("extension remote writes survived restoration")
	}
}

func checkBloemStoredEffects(t *testing.T, e *Env) {
	t.Helper()
	catalogs, err := scenariocatalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, c := range catalogs {
		for _, row := range c.Rows {
			for _, s := range row.Scenarios {
				if s.ID != "bloem_household_command.meaning" && s.ID != "bloem_device_remote.ok" {
					continue
				}
				seen++
				response, failures, err := e.exchange(e.live.URL, row.Method, s.Request, s.Principal, s.Expect, nil, nil, nil)
				if err != nil || len(failures) != 0 {
					t.Fatalf("extension write: err=%v failures=%v", err, failures)
				}
				store := remote.NewPostgresStore(e.pool)
				if s.ID == "bloem_household_command.meaning" {
					var wire remote.Command
					if err := json.Unmarshal(response.Raw, &wire); err != nil {
						t.Fatal(err)
					}
					stored, err := store.Get(e.ctx, wire.ID)
					if err != nil || stored == nil {
						t.Fatalf("command was not persisted: %v", err)
					}
					if stored.TenantID != e.fixtures["bloem_organization_id"] || wire.TenantID != stored.TenantID {
						t.Fatal("command audit retained a previous fixture organization after reseed")
					}
					if stored.TargetSessionID != bloemPlaybackID || stored.TargetUserID != e.users[fixtureMember].ID ||
						stored.TargetProfileID != profileSecondary || stored.IssuedBy != "profile:"+profilePrimary ||
						stored.IssuerKind != remote.IssuerHousehold || stored.State != remote.StateRejectedUnsupported ||
						stored.Name != playback.CommandPause || stored.SentAt != nil || stored.FinishedAt == nil {
						t.Fatal("stored audit does not match the unsupported household command")
					}
				} else {
					stored, err := store.GetDeviceCapability(e.ctx, e.users[fixtureMember].ID, profilePrimary, deviceIDA)
					if err != nil || stored == nil || stored.Version != 1 || len(stored.Commands) != 2 ||
						stored.Commands[0] != playback.CommandPause || stored.Commands[1] != playback.CommandSeek {
						t.Fatalf("device capability was not persisted for its exact owner: %v", err)
					}
					foreign, err := store.GetDeviceCapability(e.ctx, e.users[fixtureMember].ID, profileSecondary, deviceIDA)
					if err != nil || foreign != nil {
						t.Fatal("device capability escaped its profile")
					}
				}
			}
		}
	}
	if seen != 2 {
		t.Fatal("required remote effect scenarios disappeared or duplicated")
	}
}

// Keep the frozen 400 oracles intact. A dead lifecycle store fails closed
// before validation; the same request reaches the frozen oracle when ready.
func checkLifecycleValidationReadiness(t *testing.T, e *Env) {
	t.Helper()
	catalogs, err := scenariocatalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"setup.missing_fields": false, "setup.malformed_json": false, "signup.missing_fields": false}
	for _, c := range catalogs {
		for _, row := range c.Rows {
			for _, s := range row.Scenarios {
				if _, ok := wanted[s.ID]; !ok {
					continue
				}
				if wanted[s.ID] || s.Expect.Status != 400 || row.RegistrationIndex != 0 {
					t.Fatalf("frozen validation oracle changed: %s", s.ID)
				}
				wanted[s.ID] = true
				t.Run(s.ID, func(t *testing.T) {
					if _, failures, err := e.exchange(e.live.URL, row.Method, s.Request, s.Principal, s.Expect, nil, nil, nil); err != nil || len(failures) != 0 {
						t.Fatalf("ready store must preserve original validation: err=%v failures=%v", err, failures)
					}
					unavailable := scenariocatalog.Expect{Status: 503, Body: []scenariocatalog.BodyAssertion{{Pointer: "/error/code", Op: "equals", Value: json.RawMessage(`"lifecycle_idempotency_unavailable"`)}}}
					if _, failures, err := e.exchange(e.offline.URL, row.Method, s.Request, s.Principal, unavailable, nil, nil, nil); err != nil || len(failures) != 0 {
						t.Fatalf("unavailable store must fail before input validation: err=%v failures=%v", err, failures)
					}
				})
			}
		}
	}
	for id, seen := range wanted {
		if !seen {
			t.Errorf("missing original validation scenario %s", id)
		}
	}
}
