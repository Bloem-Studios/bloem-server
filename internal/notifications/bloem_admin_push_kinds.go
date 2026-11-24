package notifications

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Admin push kinds. The ids are stable keys: apps use them in payloads,
// settings and analytics (bloem-staff-push.md). The two reachability kinds are
// produced by the apps' own checks, because a server that is down cannot push;
// they are listed here so a device's per-kind switch is kept for them too.
const (
	AdminKindServerDown     = "admin.server_down"
	AdminKindServerUp       = "admin.server_up"
	AdminKindRequestWaiting = "admin.request_waiting"
	AdminKindScanFailed     = "admin.scan_failed"
	AdminKindBackupFailed   = "admin.backup_failed"
	AdminKindNodeOffline    = "admin.node_offline"
	AdminKindPluginFailed   = "admin.plugin_failed"
	// The limit kinds: someone was refused a stream or a transcode at a
	// limit. Organization admins get them for their own members.
	AdminKindStreamLimitReached    = "admin.stream_limit_reached"
	AdminKindTranscodeLimitReached = "admin.transcode_limit_reached"
)

// Admin push channels, one per app section.
const (
	AdminChannelServers   = "servers"
	AdminChannelRequests  = "requests"
	AdminChannelLibraries = "libraries"
	AdminChannelBackups   = "backups"
	AdminChannelNodes     = "nodes"
	AdminChannelPlugins   = "plugins"
	AdminChannelLimits    = "limits"
)

// Admin push actions a notification may offer. Nothing destructive is listed.
const (
	AdminActionApprove = "approve"
	AdminActionDecline = "decline"
)

// Widget channels: what the admin app's home-screen widgets can show. An app
// reports the channels its placed widgets show, and a wake names the ones that changed.
// Health is the Needs a look widget: it wakes when a backup or a plugin fails or
// recovers. Apps name it only to a server that lists AdminWidgetHealthCapabilityToken.
const (
	AdminWidgetSessions  = "sessions"
	AdminWidgetRequests  = "requests"
	AdminWidgetLibraries = "libraries"
	AdminWidgetNodes     = "nodes"
	AdminWidgetHealth    = "health"
)

// Widget wake rate limit (bloem-staff-widgets.md, "Widget wakes"), applied per device on the server.
const (
	// adminWakeCoalesce is how long changes gather into one wake.
	adminWakeCoalesce = 30 * time.Second
	// adminWakeMinGap is the least time between two wakes to one device.
	adminWakeMinGap = 10 * time.Minute
	// adminWakeAfterVisible is how long after a visible push no wake goes out, because the push already refreshes the widgets.
	adminWakeAfterVisible = 2 * time.Minute
)

var adminWidgetChannels = []string{AdminWidgetSessions, AdminWidgetRequests, AdminWidgetLibraries, AdminWidgetNodes, AdminWidgetHealth}

// AdminWidgetChannels lists the channels a widget report may name.
func AdminWidgetChannels() []string { return append([]string(nil), adminWidgetChannels...) }

func validAdminWidgetChannels(channels []string) ([]string, error) {
	known := map[string]bool{}
	for _, c := range adminWidgetChannels {
		known[c] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(channels))
	for _, c := range channels {
		if !known[c] {
			return nil, fmt.Errorf("%w: unknown widget channel %q", ErrAdminPushInvalid, c)
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out, nil
}

// AdminWakeReason is the reason every widget wake carries.
const AdminWakeReason = "widgets"

// adminWakeID is the delivery id a wake carries after the device id: the reason,
// the channels that changed (sorted, full names, no hyphens inside a name), then
// the time in Unix seconds, joined by hyphens, for example
// "widgets-nodes-requests-1763254800". A wake has no content row, so the phone reads
// the channels from the id and fetches nothing.
func adminWakeID(channels []string, at time.Time) string {
	sorted := append([]string(nil), channels...)
	sort.Strings(sorted)
	return AdminWakeReason + "-" + strings.Join(sorted, "-") + "-" + strconv.FormatInt(at.Unix(), 10)
}

// AdminPushKind describes one kind: where it shows up, what the lock screen
// says, where a tap goes and which buttons it offers.
type AdminPushKind struct {
	ID string
	// Channel is the Android channel and the iOS thread family.
	Channel string
	// LockText is all a locked screen shows unless the device allows details.
	LockText string
	// Page is the app link slug a tap opens; empty opens the server.
	Page string
	// Actions are the buttons the notification offers.
	Actions []string
	// ServerEmitted is false for kinds only the apps' local checks produce.
	ServerEmitted bool
	// OrganizationAdmins is true for the kinds an organization's own admins
	// may switch on and receive, for their organization's members only.
	OrganizationAdmins bool
}

var adminPushKinds = []AdminPushKind{
	{ID: AdminKindServerDown, Channel: AdminChannelServers, LockText: "A server can't be reached"},
	{ID: AdminKindServerUp, Channel: AdminChannelServers, LockText: "A server is back"},
	{ID: AdminKindRequestWaiting, Channel: AdminChannelRequests, LockText: "New request waiting", Page: "requests",
		Actions: []string{AdminActionApprove, AdminActionDecline}, ServerEmitted: true},
	{ID: AdminKindScanFailed, Channel: AdminChannelLibraries, LockText: "A library scan failed", Page: "libraries", ServerEmitted: true},
	{ID: AdminKindBackupFailed, Channel: AdminChannelBackups, LockText: "A backup failed", Page: "backups", ServerEmitted: true},
	{ID: AdminKindNodeOffline, Channel: AdminChannelNodes, LockText: "A node is offline", Page: "nodes", ServerEmitted: true},
	{ID: AdminKindPluginFailed, Channel: AdminChannelPlugins, LockText: "A plugin needs attention", Page: "plugins", ServerEmitted: true},
	{ID: AdminKindStreamLimitReached, Channel: AdminChannelLimits, LockText: "Someone was turned away at a stream limit", Page: "limits",
		ServerEmitted: true, OrganizationAdmins: true},
	{ID: AdminKindTranscodeLimitReached, Channel: AdminChannelLimits, LockText: "Someone was turned away at a transcode limit", Page: "limits",
		ServerEmitted: true, OrganizationAdmins: true},
}

// AdminPushKinds lists every kind in the order the apps show them.
func AdminPushKinds() []AdminPushKind {
	out := make([]AdminPushKind, len(adminPushKinds))
	copy(out, adminPushKinds)
	return out
}

// AdminPushKindByID reports the kind for id.
func AdminPushKindByID(id string) (AdminPushKind, bool) {
	for _, kind := range adminPushKinds {
		if kind.ID == id {
			return kind, true
		}
	}
	return AdminPushKind{}, false
}

// AdminPushAudience says whose devices an event goes to.
type AdminPushAudience int

const (
	// AudiencePlatformAdmins is every enabled account with the admin role:
	// infrastructure state (nodes, backups, plugins, library mounts) is
	// deployment-wide.
	AudiencePlatformAdmins AdminPushAudience = iota
	// AudienceRequestAdmins is the admins whose authority covers the request:
	// those in the requester's organization.
	AudienceRequestAdmins
	// AudienceLimitWatchers is every platform admin, plus the active admins
	// of the organization named by the notice's OrganizationID: someone was
	// refused at a limit, and their organization's admins watch its limits.
	AudienceLimitWatchers
)

// AdminPushNotice is one event ready to become a delivery.
type AdminPushNotice struct {
	Kind        string
	SubjectType string
	SubjectID   string
	// DedupeKey separates one occurrence from the next for the same subject:
	// a request id, a failed run id, or a window start. Every replica that
	// sees the same occurrence must compute the same key.
	DedupeKey string
	Title     string
	Body      string
	// Badge is the waiting-items count the app shows on its icon; nil leaves
	// the badge alone.
	Badge    *int
	Audience AdminPushAudience
	// RequesterUserID scopes AudienceRequestAdmins.
	RequesterUserID int
	// OrganizationID scopes AudienceLimitWatchers: the refused person's
	// organization, or uuid.Nil for none (platform admins only).
	OrganizationID uuid.UUID
	TTL            time.Duration
}

const (
	adminPushDefaultTTL = 24 * time.Hour
	adminTextMax        = 140
)

// adminDeliveryID derives the idempotent delivery id. The same kind, subject,
// occurrence and recipient always give the same id, so replicas and retries
// insert one row.
func adminDeliveryID(n AdminPushNotice, userID int) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("bloem-admin-push:%s:%s:%s:%s:%d",
		n.Kind, n.SubjectType, n.SubjectID, n.DedupeKey, userID))).String()
}

// adminCollapseKey is the relay collapse id: a repeated event for the same
// subject replaces the notification already showing. Down and up for one
// server share a group so each replaces the other. The relay accepts at most
// 64 bytes.
func adminCollapseKey(n AdminPushNotice) string {
	group := n.Kind
	if n.Kind == AdminKindServerDown || n.Kind == AdminKindServerUp {
		group = "admin.server_reachability"
	}
	sum := sha256.Sum256([]byte(group + "|" + n.SubjectType + "|" + n.SubjectID))
	return hex.EncodeToString(sum[:16])
}

// adminText trims a value for a notification line: one line, no control
// characters, at most adminTextMax characters.
func adminText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= adminTextMax {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:adminTextMax-1])) + "…"
}

func adminClock(t time.Time) string { return t.UTC().Format("15:04 UTC") }

// RequestWaitingNotice is "{user} requested {title}" / "{n} waiting".
func RequestWaitingNotice(requestID, requester, title string, requesterUserID, waiting int) AdminPushNotice {
	who := adminText(requester)
	if who == "" {
		who = "Someone"
	}
	n := waiting
	if n < 1 {
		n = 1
	}
	return AdminPushNotice{
		Kind: AdminKindRequestWaiting, SubjectType: "request", SubjectID: requestID, DedupeKey: requestID,
		Title: who + " requested " + adminText(title), Body: fmt.Sprintf("%d waiting", n), Badge: &n,
		Audience: AudienceRequestAdmins, RequesterUserID: requesterUserID,
	}
}

// ScanFailedNotice is "Scan failed: {library}" / reason. The window groups a
// library that keeps failing into one notification per window.
func ScanFailedNotice(libraryID, library, reason string, at time.Time, window time.Duration) AdminPushNotice {
	if window <= 0 {
		window = 6 * time.Hour
	}
	reason = adminText(reason)
	if reason == "" {
		reason = "The scan did not finish"
	}
	return AdminPushNotice{
		Kind: AdminKindScanFailed, SubjectType: "library", SubjectID: libraryID,
		DedupeKey: fmt.Sprintf("w%d", at.Unix()/int64(window.Seconds())),
		Title:     "Scan failed: " + adminText(library), Body: reason, Audience: AudiencePlatformAdmins,
	}
}

// BackupFailedNotice is "Backup failed on {server}" / reason, once per failed run.
func BackupFailedNotice(runID, server, reason string) AdminPushNotice {
	reason = adminText(reason)
	if reason == "" {
		reason = "The backup did not finish"
	}
	server = adminText(server)
	if server == "" {
		server = "this server"
	}
	return AdminPushNotice{
		Kind: AdminKindBackupFailed, SubjectType: "backup", SubjectID: "backups", DedupeKey: runID,
		Title: "Backup failed on " + server, Body: reason, Audience: AudiencePlatformAdmins,
	}
}

// NodeOfflineNotice is "{node} is offline" / "Since {time}", once per outage.
func NodeOfflineNotice(nodeID, node string, since time.Time) AdminPushNotice {
	return AdminPushNotice{
		Kind: AdminKindNodeOffline, SubjectType: "node", SubjectID: nodeID,
		DedupeKey: fmt.Sprintf("s%d", since.Unix()),
		Title:     adminText(node) + " is offline", Body: "Since " + adminClock(since), Audience: AudiencePlatformAdmins,
	}
}

// PluginFailedNotice is "{plugin} needs attention" / reason, once per failure
// streak (the streak start is its occurrence).
func PluginFailedNotice(installationID, plugin, reason string, streakStart time.Time) AdminPushNotice {
	reason = adminText(reason)
	if reason == "" {
		reason = "The plugin stopped"
	}
	return AdminPushNotice{
		Kind: AdminKindPluginFailed, SubjectType: "plugin", SubjectID: installationID,
		DedupeKey: fmt.Sprintf("s%d", streakStart.Unix()),
		Title:     adminText(plugin) + " needs attention", Body: reason, Audience: AudiencePlatformAdmins,
	}
}

// limitScopeWords names a transcode limit's scope in a notice body.
var limitScopeWords = map[string]string{"person": "personal", "household": "household", "organization": "organization's"}

// limitNoticeHour groups a person's refusals into one notification per
// recipient per UTC hour.
func limitNoticeHour(at time.Time) string { return at.UTC().Format("2006-01-02T15") }

func limitPerson(person string) string {
	if who := adminText(person); who != "" {
		return who
	}
	return "Someone"
}

// StreamLimitNotice is "Stream limit reached" / "{person} was turned away:
// all {limit} streams in use.", once per person per recipient per hour.
func StreamLimitNotice(userID int, person string, limit int, scope string, at time.Time) AdminPushNotice {
	_ = scope
	return AdminPushNotice{
		Kind: AdminKindStreamLimitReached, SubjectType: "person", SubjectID: strconv.Itoa(userID), DedupeKey: limitNoticeHour(at),
		Title: "Stream limit reached", Body: fmt.Sprintf("%s was turned away: all %d streams in use.", limitPerson(person), limit),
		Audience: AudienceLimitWatchers,
	}
}

// TranscodeLimitNotice is "Transcode limit reached" / "{person} couldn't
// start a transcode: the {scope} limit of {limit} is in use.", once per person
// per recipient per hour.
func TranscodeLimitNotice(userID int, person string, limit int, scope string, at time.Time) AdminPushNotice {
	word := limitScopeWords[scope]
	if word == "" {
		word = "personal"
	}
	return AdminPushNotice{
		Kind: AdminKindTranscodeLimitReached, SubjectType: "person", SubjectID: strconv.Itoa(userID), DedupeKey: limitNoticeHour(at),
		Title:    "Transcode limit reached",
		Body:     fmt.Sprintf("%s couldn't start a transcode: the %s limit of %d is in use.", limitPerson(person), word, limit),
		Audience: AudienceLimitWatchers,
	}
}
