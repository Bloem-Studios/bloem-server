package executor

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
	"github.com/Silo-Server/silo-server/internal/secret"
)

const (
	bloemDirectEmail    = "fixture-direct@silo.example.test"
	bloemDirectPassword = "fixture-direct-profile-password"
	bloemDirectDevice   = "fixture-direct-device"
	bloemPlaybackID     = "00000000-0000-4000-8000-00000000bf01"
	bloemForeignID      = "00000000-0000-4000-8000-00000000bf02"
)

// withBloemRowFixture supplies only the five retained v1 extension rows.
// Catalogs remain unpaired: these are not invented Silo v2 operations.
// The regular live/offline wiring and every pre-existing row stay unchanged.
func (e *Env) withBloemRowFixture(row scenariocatalog.Row) func() {
	noop := func() {}
	if !e.HasDatabase() {
		return noop
	}
	remoteRow := false
	switch row.Method + " " + row.Path {
	case "GET /api/v1/profiles/{id}", "POST /api/v1/auth/profile-login":
	case "POST /api/v1/profiles/household/sessions/{session_id}/commands", "PUT /api/v1/devices/{device_id}/remote-control":
		remoteRow = true
	default:
		return noop
	}

	previousOverlay, previousLive := e.afterReseed, e.live
	var sessions *playback.SessionManager
	var closeRemote func()
	if remoteRow {
		// Audit rows outlive users. Refuse occupied fixture IDs before any
		// cleanup; teardown deletes only commands created for these sessions.
		var occupied int
		if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM remote_commands WHERE target_session_id IN ($1, $2)`, bloemPlaybackID, bloemForeignID).Scan(&occupied); err != nil {
			e.t.Fatal(err)
		}
		if occupied != 0 {
			e.t.Fatal("Bloem remote fixture session IDs already have audit rows")
		}
		sessions = playback.NewSessionManager(0, 0)
		ctx, cancel := context.WithCancel(e.ctx)
		cfg := e.config()
		cfg.Playback.TranscodeDir = e.t.TempDir()
		cipher, err := secret.New([]byte(masterKey))
		if err != nil {
			e.t.Fatal(err)
		}
		server := httptest.NewServer(api.NewRouter(api.Dependencies{
			Config: cfg, AppContext: ctx, DB: e.pool, SecretCipher: cipher,
			ClientIPResolver: clientip.NewResolver(nil), NodeID: "fixture-node", PublicURL: publicURL,
			UserStoreProvider: e.stores, PolicySystem: e.policy, SessionMgr: sessions,
		}))
		e.live = server
		closeRemote = func() { server.Close(); cancel() }
	}

	e.afterReseed = func() {
		if previousOverlay != nil {
			previousOverlay()
		}
		credentials := auth.NewProfileCredentialService(e.pool)
		member := e.users[fixtureMember]
		if err := credentials.Set(e.ctx, member.ID, profileSecondary, bloemDirectEmail, bloemDirectPassword); err != nil {
			e.t.Fatalf("Bloem direct credential fixture: %v", err)
		}
		e.auth.SetProfileCredentialService(credentials)
		pair, subject, err := e.auth.LoginProfile(e.ctx, bloemDirectEmail, bloemDirectPassword, auth.DeviceClaim{ID: bloemDirectDevice})
		if err != nil {
			e.t.Fatalf("Bloem direct session fixture: %v", err)
		}
		e.fixtures["bloem_direct_email"] = bloemDirectEmail
		e.fixtures["bloem_direct_password"] = bloemDirectPassword
		e.fixtures["bloem_direct_device"] = bloemDirectDevice
		e.fixtures["bloem_direct_token"] = pair.AccessToken
		e.fixtures["bloem_organization_id"] = subject.OrganizationID
		e.fixtures["bloem_membership_id"] = subject.MembershipID
		if sessions != nil {
			e.mustExec(`DELETE FROM remote_commands WHERE target_session_id IN ($1, $2)`, bloemPlaybackID, bloemForeignID)
			// These are synthetic playback records with no media and no socket.
			// The oracle asserts unsupported/refused commands, never delivery.
			for _, s := range []*playback.Session{
				{ID: bloemPlaybackID, UserID: member.ID, ProfileID: profileSecondary, DeviceID: "fixture-device-c", TenantID: subject.OrganizationID},
				{ID: bloemForeignID, UserID: e.users[fixtureAdmin].ID, ProfileID: profileAdminPrimary, DeviceID: "fixture-admin-device", TenantID: subject.OrganizationID},
			} {
				// Reconstruction preserves an existing session. Drop only this
				// fixture's old record so a reseed cannot retain its former tenant.
				if err := sessions.StopSession(s.ID); err != nil && !errors.Is(err, playback.ErrSessionNotFound) {
					e.t.Fatalf("Bloem playback fixture reset: %v", err)
				}
				sessions.RegisterReconstructed(s)
			}
			e.fixtures["bloem_playback_session"] = bloemPlaybackID
			e.fixtures["bloem_foreign_session"] = bloemForeignID
		}
	}
	return func() {
		if closeRemote != nil {
			closeRemote()
			e.mustExec(`DELETE FROM remote_commands WHERE target_session_id IN ($1, $2)`, bloemPlaybackID, bloemForeignID)
		}
		e.afterReseed, e.live = previousOverlay, previousLive
		e.auth.SetProfileCredentialService(nil)
		for key := range e.fixtures {
			if strings.HasPrefix(key, "bloem_") {
				delete(e.fixtures, key)
			}
		}
		e.Reseed()
	}
}
