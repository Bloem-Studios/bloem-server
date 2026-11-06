package backup

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Bloem's tables, switches, per-user tables and settings, classified for
// Silo's backup and restore the way tables.go, credentials.go,
// credentials_stricter.go, users_registry.go and the settings files classify
// Silo's. init adds them to Silo's lists, so those lists merge from Silo
// unchanged. docs/architecture/bloem-backup.md describes the decisions.
func init() {
	restoredDataTables = append(restoredDataTables, bloemRestoredDataTables...)
	serverStateTables = append(serverStateTables, bloemServerStateTables...)
	loggedTables = append(loggedTables, bloemLoggedTables...)
	// Carried tables are replaced in list order; Bloem's owner conditions
	// read only tables the restore brings back whole, so they go last.
	secretTables = append(secretTables, bloemSecretTables...)
	stricterFlags = append(stricterFlags, bloemStricterFlags()...)
	maps.Copy(restoredFlagColumns, bloemRestoredFlagColumns)

	accountFieldTables = append(accountFieldTables, bloemAccountFieldTables...)
	maps.Copy(excludedUserTables, bloemExcludedUserTables)

	serverSpecificSettings = append(serverSpecificSettings, bloemServerSpecificSettings...)
	restoredSettings = append(restoredSettings, bloemRestoredSettings...)
	for _, key := range bloemStricterSettings {
		accessSettings[key] = on
	}
	settingsPages = append(settingsPages, bloemSettingsPages...)
	for key, rule := range bloemSettingRules {
		settingRules[key] = rule
	}
	// Bloem keeps user databases as node-local SQLite: the Silo user-DB S3
	// credentials are not Bloem settings (internal/catalog's
	// bloem_encrypted_settings.go drops them from SensitiveSettingKeys too).
	restoredSecretSettings = slices.DeleteFunc(restoredSecretSettings, func(key string) bool {
		return key == "s3.user_db_access_key" || key == "s3.user_db_secret_key"
	})
}

// bloemRestoredDataTables are Bloem's data: organizations and their
// memberships, entitlements and policy, branding and promotions, book
// requests, storage-backed library configuration, and the Live TV setup.
// Accounts roll back whole with their organizations.
var bloemRestoredDataTables = strings.Fields(`
	organizations organization_memberships organization_entitlements resource_owners platform_security
	entitlement_bundles entitlement_bundle_versions entitlement_bundle_members
	entitlement_templates entitlement_template_revisions entitlement_policy_cohorts entitlement_policy_cohort_revisions
	admin_audit_events entitlement_audit_events
	bloem_organization_branding bloem_organization_domains ambience_packs ambience_assets promotions
	notification_announcements bloem_upgrade_prompt
	bloem_book_requests bloem_audiobook_request_integrations bloem_ebook_request_integrations
	library_storage_locations bloem_storage_sources bloem_storage_installations bloem_storage_selected_roots
	livetv_tuners livetv_channels livetv_guide_sources livetv_series_rules livetv_recordings
`)

// bloemServerStateTables describe the running server: job and lease state,
// receipts, counters, caches, the storage scan index (rebuilt by the next
// scan), the Live TV guide (refetched from its sources), and sessions.
var bloemServerStateTables = strings.Fields(`
	admin_people_bulk_jobs admin_people_bulk_job_receipts admin_people_bulk_targets admin_people_selections
	bloem_audiobook_request_acquisitions bloem_audiobook_request_decisions bloem_ebook_request_dispatches
	bloem_ebook_request_policy_epoch bloem_library_location_state bloem_livetv_xtream_leases
	bloem_org_session_revocations bloem_preview_event_counts bloem_preview_personal_presentation
	bloem_preview_ticket_uses bloem_silo_switching
	bloem_storage_audio_probes bloem_storage_audio_staged_tracks bloem_storage_change_tokens bloem_storage_entries
	bloem_storage_file_refs bloem_storage_file_roots bloem_storage_location_directories bloem_storage_location_edges
	bloem_storage_location_entries bloem_storage_scan_cursors bloem_storage_scan_directories
	bloem_storage_scan_directory_entries bloem_storage_scan_directory_roots bloem_storage_scan_runs
	ebook_cover_retries entitlement_apply_receipts livetv_artwork_cache livetv_programs livetv_sessions
	login_email_registry playback_capacity_reservations remote_device_capabilities
	resource_tenancy_migration_ledger tenant_member_command_receipts userdb_sqlite_owner
`)

// bloemLoggedTables are queues the dump leaves out: remote commands to
// devices expire within minutes.
var bloemLoggedTables = []string{"remote_commands"}

// previewGuests is why the preview guest tables are left out.
const previewGuests = "preview guest sign-ins and the playback they were granted"

// bloemSecretTables are Bloem's tables holding a sign-in, a credential for
// an outside service, or a key that admits someone.
var bloemSecretTables = []secretTable{
	// Guest sessions name auth_sessions, which the dump leaves out; grants
	// and their playback sessions go with them.
	{name: "bloem_preview_guest_sessions", handling: secretLeftOut, why: previewGuests},
	{name: "bloem_preview_play_grants", handling: secretLeftOut, why: previewGuests},
	{name: "bloem_preview_play_sessions", handling: secretLeftOut, why: previewGuests},
	// Room owners name watch_together_rooms, which the dump leaves out.
	{name: "watch_together_room_owners", handling: secretLeftOut, why: watchTogether},
	// An Xtream sign-in changed or removed since the backup stays as the
	// live server has it, and the backup's never reaches the provider again.
	{name: "bloem_livetv_xtream_credentials", handling: secretCarried, why: "Xtream provider sign-ins of Live TV tuners", carry: &carrySpec{
		key: "tuner_id", same: []string{"tuner_id"}, credential: everyRow,
		owned:    `EXISTS (SELECT 1 FROM livetv_tuners x WHERE x.id = r.tuner_id)`,
		describe: `format('Xtream sign-in of tuner %s', r.tuner_id)`,
	}},
	// The storefront's ticket keys decide who may open a preview: a key
	// retired since the backup must not admit anyone again.
	{name: "bloem_preview_settings", handling: secretCarried, why: "preview storefront settings and the ticket keys that admit guests", carry: &carrySpec{
		key: "singleton", same: []string{"singleton"}, credential: everyRow,
		owned:    `(r.account_id IS NULL OR EXISTS (SELECT 1 FROM users u WHERE u.id = r.account_id))`,
		describe: `'preview settings'`,
	}},
}

// bloemStricterFlags are Bloem's switches a restore keeps at the stricter of
// the backup's and the live value. A row removed since the backup comes back
// off, as Silo's own flags do.
func bloemStricterFlags() []stricterFlag {
	flags := []stricterFlag{
		{table: "access_groups", column: "playback_allowed", stricter: false, key: `t.name`, scope: everyRow,
			describe: `format('access group %s kept without playback', t.name)`},
		// A storage source or root an admin turned off is not read again.
		{table: "bloem_storage_sources", column: columnEnabled, stricter: false, id: "t.key::text", key: noKey, scope: everyRow,
			describe: `format('storage source %s kept off', t.key)`},
		flag("bloem_storage_selected_roots", columnEnabled, false, "storage root"),
		// A channel an admin hid stays hidden; a guide source turned off is
		// not fetched.
		flag("livetv_channels", columnEnabled, false, "Live TV channel"),
		flag("livetv_guide_sources", columnEnabled, false, "Live TV guide source"),
	}
	for i := range flags {
		f := &flags[i]
		f.absentOff = true
		f.describeAbsent = fmt.Sprintf(`format('%s %%s restored with %s off: removed after the backup', %s)`, f.table, f.column, f.idOf())
	}
	return flags
}

// Reasons a Bloem boolean column is restored as the backup has it.
const (
	whyRevision      = "an immutable revision; the access group it was applied to keeps its own switches"
	whyOrganizations = "organizations roll back whole with their accounts"
)

// bloemRestoredFlagColumns are the boolean columns of Bloem's restored
// tables that are not stricter flags, with why the backup's value is kept.
var bloemRestoredFlagColumns = map[string]string{
	"ambience_packs.repeat_yearly":                                   whyShown,
	"bloem_organization_domains.is_primary":                          "which host an organization's links use",
	"bloem_upgrade_prompt.enabled":                                   whyShown,
	"bloem_upgrade_prompt.id":                                        whyKey,
	"entitlement_bundles.is_organization_creation_default":           "the bundle an operator's new organization starts with; " + whyOrganizations,
	"entitlement_policy_cohort_revisions.download_allowed":           whyRevision,
	"entitlement_policy_cohort_revisions.download_transcode_allowed": whyRevision,
	"entitlement_policy_cohort_revisions.playback_allowed":           whyRevision,
	"entitlement_policy_cohort_revisions.requests_allowed":           whyRevision,
	"entitlement_policy_cohort_revisions.transcode_allowed":          whyRevision,
	"entitlement_policy_cohorts.archived":                            "an archived cohort takes no new accounts",
	"entitlement_template_revisions.download_allowed":                whyRevision,
	"entitlement_template_revisions.download_transcode_allowed":      whyRevision,
	"entitlement_template_revisions.playback_allowed":                whyRevision,
	"entitlement_template_revisions.requests_allowed":                whyRevision,
	"entitlement_template_revisions.transcode_allowed":               whyRevision,
	"entitlement_templates.archived":                                 "an archived template is offered to no one",
	"entitlement_templates.bloem_preview_seeded":                     whyMigration,
	"entitlement_templates.enabled":                                  "whether admins may apply it; applying is an admin action",
	"livetv_channels.hd":                                             whyFacts,
	"livetv_recordings.interrupted":                                  whyHistory,
	"livetv_series_rules.enabled":                                    "a profile's own recording rule",
	"livetv_series_rules.new_only":                                   "what a profile's recording rule records",
	"organizations.is_default":                                       "the default organization; exactly one",
	"platform_security.ownership_resolution_required":                "the platform owner rolls back with the accounts",
	"platform_security.singleton":                                    whyKey,
	"promotions.dismissible":                                         whyShown,
}

// bloemAccountFieldTables are kept from the account and profile fields:
// login_email_registry is kept by triggers on users and user_profiles.
var bloemAccountFieldTables = []string{"login_email_registry"}

// bloemExcludedUserTables are Bloem's per-user tables a restore set and a
// profile export leave out.
var bloemExcludedUserTables = map[string]string{
	"livetv_recordings":              "Live TV recordings, restored with the server's Live TV setup",
	"livetv_series_rules":            "Live TV recording rules, restored with the server's Live TV setup",
	"livetv_sessions":                "Live TV tuner sessions in progress",
	"playback_capacity_reservations": excludedPlayback,
	"remote_device_capabilities":     "what a device reports each time it connects",
}

// bloemServerSpecificSettings describe this server: where it records Live
// TV and its hardware.
var bloemServerSpecificSettings = strings.Fields(`livetv.dvr_path livetv.hw_accel livetv.hw_decode`)

// bloemRestoredSettings are restored as the backup has them: how Live TV
// plays and transcodes.
var bloemRestoredSettings = strings.Fields(`livetv.`)

// bloemStricterSettings are switches a restore keeps at the stricter value:
// announcing the server on the LAN (as server.lan_discovery), and whether
// accounts may request ebooks (as Silo's request switches).
var bloemStricterSettings = []string{"lan.advertisement_enabled", "bloem.requests.ebooks_enabled"}

// pageLiveTV is Bloem's Live TV settings page.
const pageLiveTV = "livetv"

var bloemSettingsPages = []SettingsPage{{pageLiveTV, "Live TV"}}

var bloemSettingRules = map[string]settingRule{
	"lan.advertisement_enabled":     {page: "general", class: SettingPortable},
	"bloem.requests.ebooks_enabled": {page: pageRequests, class: SettingPortable},
	"livetv.":                       {page: pageLiveTV, class: SettingPortable},
	"livetv.dvr_path":               {page: pageLiveTV, class: SettingServerSpecific},
	"livetv.hw_accel":               {page: pageLiveTV, class: SettingServerSpecific},
	"livetv.hw_decode":              {page: pageLiveTV, class: SettingServerSpecific},
}
