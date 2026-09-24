package executor

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/ratelimit"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// bloemEnv is the Bloem state embedded in Env.
type bloemEnv struct {
	// beforeReseed removes only a library owned by a row fixture before the
	// unchanged scratch-database guard inspects the remaining database.
	// (afterReseed supplies an active row or dedicated test's prerequisite
	// state.)
	beforeReseed func()
	// Router maintenance is canceled before test cleanup closes its pools. Fixture
	// SQL keeps ctx separately because cleanup hooks still restore owned rows.
	appCtx context.Context

	// reseedStats accumulates Reseed timings; logged when the env closes.
	reseedStats reseedStats
	reseedLap   time.Time
	// snapshot holds the household the first Reseed built; nil until then,
	// and nil for good when the database cannot restore one (snapshot.go).
	snapshot         *householdSnapshot
	snapshotDisabled bool
	// seedingFixtures holds the real fixture map while a seeding Reseed
	// captures the placeholders the household sets.
	seedingFixtures map[string]string
}

// bloemOfflineAppContext is the offline router's application context. Its
// background workers (history import queue and the like) can only ever fail
// against the dead pool, so they get an already canceled application context
// and exit at once instead of retrying for the whole run. Request handling
// does not read AppContext.
func bloemOfflineAppContext() context.Context {
	offlineAppCtx, cancelOffline := context.WithCancel(context.Background())
	cancelOffline()
	return offlineAppCtx
}

// bloemRegisterCleanups registers the Env's Bloem cleanups; they run before
// the pool closes.
func (e *Env) bloemRegisterCleanups() {
	e.t.Cleanup(func() { e.t.Logf("scenario executor timing: %s", &e.reseedStats) })
	e.t.Cleanup(e.dropSnapshot)
}

// bloemFinalizeFixtureMembership finalizes the membership policy authority on
// the freshly migrated scratch database.
func (e *Env) bloemFinalizeFixtureMembership(pool *pgxpool.Pool) {
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(e.ctx, pool); err != nil {
		e.t.Fatalf("scenario executor: finalize fixture membership policy: %v", err)
	}
}

func bloemFixtureMembershipsFor(pool *pgxpool.Pool) bloemFixtureMemberships {
	return bloemFixtureMemberships{store: tenancy.NewStore(pool)}
}

// bloemActivateFixtureOwnership makes the fixture admin the initial owner.
func (e *Env) bloemActivateFixtureOwnership(admin *models.User) {
	if _, err := tenancy.NewStore(e.pool).ActivateInitialOwnership(e.ctx, admin.ID); err != nil {
		e.t.Fatalf("scenario executor: activate fixture ownership: %v", err)
	}
}

// bloemDefaultOrganizationID is the organization fixture groups belong to.
func (e *Env) bloemDefaultOrganizationID() uuid.UUID {
	organization, err := tenancy.NewStore(e.pool).DefaultOrganization(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return organization.ID
}

// bloemReseedTimer starts Reseed's timing; the returned func records the
// total and the final "hooks" phase.
func (e *Env) bloemReseedTimer() func() {
	started := time.Now()
	e.reseedLap = started
	return func() {
		e.bloemReseedMark("hooks")
		e.reseedStats.add("total", time.Since(started))
		e.reseedStats.count++
	}
}

func (e *Env) bloemReseedMark(phase string) {
	now := time.Now()
	e.reseedStats.add(phase, now.Sub(e.reseedLap))
	e.reseedLap = now
}

// bloemReseedRestored runs the start of every Reseed and, once the first
// Reseed has snapshotted the household, the whole restore path.
//
// The first Reseed of an Env builds the household through the real
// repositories (bcrypt and all) and snapshots the rows it produced (see
// snapshot.go). Later calls truncate the same tables and restore those rows
// verbatim, which is the same end state without re-running the seeding code.
// Time-relative rows (device-login requests) and settings are rewritten on
// every call, and the afterReseed hooks always run. It reports false when
// Reseed must seed from scratch.
func (e *Env) bloemReseedRestored() bool {
	e.t.Helper()
	if e.beforeReseed != nil {
		e.beforeReseed()
	}
	if e.snapshot == nil {
		return false
	}
	e.guardScratchDatabase()
	e.bloemReseedMark("guard")
	e.restoreSnapshot()
	e.bloemReseedMark("truncate")
	e.reseedSettings()
	e.bloemReseedMark("settings")
	e.restoreHouseholdState()
	e.bloemReseedMark("household")
	e.seedDeviceLogins()
	e.fixtures["locked_profile_token"] = e.mintProfileToken(e.users[fixtureMember], profileLocked)
	e.bloemReseedMark("rows")
	if e.afterReseed != nil {
		e.afterReseed()
	}
	return true
}

// bloemAfterReseedSettings ends a seeding Reseed's settings phase and starts
// capturing the placeholders the household sets.
func (e *Env) bloemAfterReseedSettings() {
	e.t.Helper()
	e.reseedBloemSettings()
	e.bloemReseedMark("settings")
	e.seedingFixtures = e.fixtures
	e.fixtures = map[string]string{}
}

// bloemSnapshotHousehold ends the capture and snapshots the seeded
// household (device-login rows excluded; they are rewritten every Reseed).
func (e *Env) bloemSnapshotHousehold() {
	e.t.Helper()
	captured := e.fixtures
	e.fixtures = e.seedingFixtures
	e.seedingFixtures = nil
	maps.Copy(e.fixtures, captured)
	e.takeSnapshot(maps.Clone(captured))
	e.bloemReseedMark("household")
}

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
	e.mustSetting("signup.enabled", "true")
	// Media requests on, so the onboarding flow's requests step is present
	// for non-child profiles and the child filter has something to remove.
	e.mustExec(`INSERT INTO request_settings (id, requests_enabled) VALUES (true, true)
		ON CONFLICT (id) DO UPDATE SET requests_enabled = true`)
	// Minted lazily per fixture state; see impersonationToken.
	delete(e.fixtures, "impersonation_token")
	e.reseedBloemSettings()
}

// reseedBloemSettings is the Bloem addition to the fixture's settings phase.
func (e *Env) reseedBloemSettings() {
	e.t.Helper()
	// Invitation links read the persisted setting, not Dependencies.PublicURL.
	// Each reseed restores the fixture's canonical external URL; scenarios
	// testing missing configuration can still override it explicitly.
	e.mustSetting("server.public_url", publicURL)
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
