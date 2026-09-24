package executor

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/ratelimit"
)

// householdRoots are the tables Reseed truncates. The CASCADE reaches every
// table that references them, directly or transitively; snapshot.go restores
// exactly that closure.
// deviceLoginTable holds rows that expire relative to now; see seedDeviceLogins.
const deviceLoginTable = "device_login_requests"

var householdRoots = []string{"users", deviceLoginTable, "invite_codes", "invitations", "access_groups"}

// reseedSettings restores the fixture's server settings and the per-state
// placeholders. Settings live outside the truncated closure, so this runs on
// every Reseed, restored or not.
func (e *Env) reseedSettings() {
	e.t.Helper()
	ctx := e.ctx
	e.mustExec(`DELETE FROM server_settings WHERE key IN ('demo.enabled','signup.enabled','server.public_url','branding.server_name','sections.allow_profile_custom_sections')`)
	if err := ratelimit.SeedDefaults(ctx, e.settings); err != nil {
		e.t.Fatalf("scenario executor: seed rate limits: %v", err)
	}
	if err := e.limiter.Reload(ctx); err != nil {
		e.t.Fatalf("scenario executor: reload rate limits: %v", err)
	}
	e.mustSetting("branding.server_name", serverName)
	// Invitation links read the persisted setting, not Dependencies.PublicURL.
	// Each reseed restores the fixture's canonical external URL; scenarios
	// testing missing configuration can still override it explicitly.
	e.mustSetting("server.public_url", publicURL)
	e.mustSetting("signup.enabled", "true")
	// Media requests on, so the onboarding flow's requests step is present
	// for non-child profiles and the child filter has something to remove.
	e.mustExec(`INSERT INTO request_settings (id, requests_enabled) VALUES (true, true)
		ON CONFLICT (id) DO UPDATE SET requests_enabled = true`)
	// Minted lazily per fixture state; see impersonationToken.
	delete(e.fixtures, "impersonation_token")
	// V2 invite-code creation requires a caller-selected value. Both the
	// generic runner and the dedicated effect tests execute these packets.
	e.fixtures["caller_invite_code"] = strings.ToUpper(rand.Text()[:8])

}

// seedDeviceLogins inserts device-login requests in every state the handlers
// distinguish. They expire relative to now, so they are rewritten on every
// Reseed rather than restored from the snapshot.
func (e *Env) seedDeviceLogins() {
	e.t.Helper()
	member := e.users[fixtureMember]
	insertDeviceLogin := func(id, dev, browser, user, status, purpose string, temporary bool, expires string) {
		e.mustExec(`INSERT INTO device_login_requests
			(id, device_code_hash, browser_code_hash, user_code_hash, match_code, device_name, device_platform, ip_address,
			 status, client_purpose, temporary, expires_at)
			VALUES ($1, $2, $3, $4, '42', 'Fixture TV', 'tvos', '127.0.0.1', $5, $6, $7, now() + $8::interval)`,
			id, hashDevice(dev), hashDevice(browser), hashDevice(user), status, purpose, temporary, expires)
	}
	insertDeviceLogin("00000000-0000-4000-8000-0000000000e1", deviceCode, browserCode, userCode, "pending", "device_login", false, "10 minutes")
	insertDeviceLogin("00000000-0000-4000-8000-0000000000e2", deviceCodeRem, browserCodeRm, userCodeRem, "pending", "remote_playback", true, "10 minutes")
	insertDeviceLogin("00000000-0000-4000-8000-0000000000e3", deviceCodeDen, browserCodeDn, userCodeDen, "denied", "device_login", false, "10 minutes")
	insertDeviceLogin("00000000-0000-4000-8000-0000000000e4", deviceCodeExp, browserCodeEx, userCodeExp, "pending", "device_login", false, "-1 minutes")
	insertDeviceLogin("00000000-0000-4000-8000-0000000000e5", deviceCodeApp, browserCodeAp, userCodeApp, "approved", "device_login", false, "10 minutes")
	e.mustExec(`UPDATE device_login_requests SET approved_by_user_id = $1, approved_at = now() WHERE id = '00000000-0000-4000-8000-0000000000e5'`, member.ID)
	insertDeviceLogin("00000000-0000-4000-8000-0000000000e6", deviceCodeRA, browserCodeRA, userCodeRA, "approved", "remote_playback", true, "10 minutes")
	e.mustExec(`UPDATE device_login_requests SET approved_by_user_id = $1, approved_profile_id = $2, approved_at = now() WHERE id = '00000000-0000-4000-8000-0000000000e6'`, member.ID, profilePrimary)

}

// reseedStats is Reseed's timing breakdown by phase.
type reseedStats struct {
	count  int
	phases map[string]time.Duration
	order  []string
}

func (s *reseedStats) add(phase string, d time.Duration) {
	if s.phases == nil {
		s.phases = map[string]time.Duration{}
	}
	if _, ok := s.phases[phase]; !ok {
		s.order = append(s.order, phase)
	}
	s.phases[phase] += d
}

func (s *reseedStats) String() string {
	if s.count == 0 {
		return "reseeds=0"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "reseeds=%d", s.count)
	for _, phase := range s.order {
		d := s.phases[phase]
		fmt.Fprintf(&b, " %s=%s(avg %s)", phase, d.Round(time.Millisecond), (d / time.Duration(s.count)).Round(time.Microsecond))
	}
	return b.String()
}
