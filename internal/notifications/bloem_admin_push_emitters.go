package notifications

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/requests"
)

// Emitters turn the events Bloem already has into admin push notices. None of
// them edits a Silo file: requests use a decorator the router installs, scan
// failures come off the events hub, and backup, node, plugin and mount state
// is read by a poller. A poller reads state every replica can see, and the
// notice's delivery id is derived from the event, so replicas that all notice
// the same thing store it once.

const (
	adminWatchEvery      = time.Minute
	adminNodeGrace       = 2 * time.Minute
	adminBackupFreshness = 6 * time.Hour
	adminOutageBucket    = 10 * time.Minute
	adminScanWindow      = 6 * time.Hour
)

// ---- Requests -------------------------------------------------------------

// adminNoticeSink is what the request decorator needs from the push service.
type adminNoticeSink interface {
	Notify(context.Context, AdminPushNotice) error
	Wake(ctx context.Context, channel string, requesterUserID int) error
}

type adminPushRequestNotifier struct {
	requests.LifecycleNotifier
	push    adminNoticeSink
	waiting func(ctx context.Context, requesterUserID int) int
	// done is called after each detached push, so a test can wait on it.
	done func()
}

// NewAdminPushRequestNotifier decorates the request lifecycle notifier so a
// request that now waits for an administrator also pushes to admin devices.
// next may be nil. waiting reports how many requests wait for the requester's
// administrators and may be nil. Without a push service it returns next as is.
func NewAdminPushRequestNotifier(next requests.LifecycleNotifier, push *AdminPushService, waiting func(ctx context.Context, requesterUserID int) int) requests.LifecycleNotifier {
	if push == nil {
		return next
	}
	return &adminPushRequestNotifier{LifecycleNotifier: next, push: push, waiting: waiting}
}

// RequestSubmitted forwards to the wrapped notifier, wakes the requests widgets, then
// pushes when the request is pending. An auto-approved request waits for nobody, so it
// wakes the widgets but is not notified.
func (n *adminPushRequestNotifier) RequestSubmitted(ctx context.Context, req requests.Request) {
	if n.LifecycleNotifier != nil {
		n.LifecycleNotifier.RequestSubmitted(ctx, req)
	}
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	go func() {
		defer cancel()
		if n.done != nil {
			defer n.done()
		}
		// Any arriving request changes what the requests widget shows, pending or auto-approved.
		if err := n.push.Wake(detached, AdminWidgetRequests, req.RequestedByUserID); err != nil {
			slog.WarnContext(detached, "admin widget wake for a request failed", "request_id", req.ID, "error", err)
		}
		// Only a request that waits for an administrator is notified.
		if req.Status != requests.StatusPending {
			return
		}
		waiting := 1
		if n.waiting != nil {
			if count := n.waiting(detached, req.RequestedByUserID); count > 0 {
				waiting = count
			}
		}
		notice := RequestWaitingNotice(req.ID, requesterName(req), req.Title, req.RequestedByUserID, waiting)
		if err := n.push.Notify(detached, notice); err != nil {
			slog.WarnContext(detached, "admin push for a waiting request failed", "request_id", req.ID, "error", err)
		}
	}()
}

func requesterName(req requests.Request) string {
	if req.RequesterUsername != "" {
		return req.RequesterUsername
	}
	return req.RequesterEmail
}

// ---- Scan failures --------------------------------------------------------

// adminReason shortens a failure message to a notification line without file
// paths, which would put a library's storage layout on a lock screen.
func adminReason(message, fallback string) string {
	fields := strings.Fields(message)
	kept := fields[:0]
	for _, field := range fields {
		trimmed := strings.Trim(field, `"'()[]:,;`)
		if strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, `\`) || strings.HasPrefix(trimmed, "~") {
			continue
		}
		kept = append(kept, field)
	}
	out := adminText(strings.Join(kept, " "))
	if out == "" {
		return fallback
	}
	return out
}

func (s *AdminPushService) libraryName(ctx context.Context, id int) string {
	var name string
	if err := s.store.pool.QueryRow(ctx, `SELECT name FROM media_folders WHERE id = $1`, id).Scan(&name); err != nil {
		return "a library"
	}
	return name
}

// hubWakeChannel maps a hub event to the widget channel it changes, or "" for none. Session starts,
// pauses, resumes and stops change the now-playing widget; a scan starting, ending (completed,
// cancelled) or failing changes the libraries widget. Progress ticks are not changes.
func hubWakeChannel(channel evt.EventChannel, event string) string {
	switch channel {
	case evt.ChannelSessions:
		switch event {
		case "session.started", "session.paused", "session.resumed", "session.stopped":
			return AdminWidgetSessions
		}
	case evt.ChannelScans:
		switch event {
		case "scan.started", "scan.completed", "scan.failed", "scan.cancelled":
			return AdminWidgetLibraries
		}
	}
	return ""
}

// WatchEvents turns events-hub events into admin pushes: a failed scan notifies admins, and the
// session and scan events above wake the widgets that show them.
func (s *AdminPushService) WatchEvents(ctx context.Context, hub *evt.Hub) {
	if !s.Available() || hub == nil {
		return
	}
	events, cancel := hub.Subscribe()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-events:
			if !ok {
				return
			}
			if channel := hubWakeChannel(env.Channel, env.Event); channel != "" {
				if err := s.Wake(ctx, channel, 0); err != nil && ctx.Err() == nil {
					s.logger.WarnContext(ctx, "admin widget wake failed", "channel", channel, "error", err)
				}
			}
			if env.Channel != evt.ChannelScans || !strings.HasPrefix(env.Event, "scan.") {
				continue
			}
			var run evt.ScanRun
			if err := json.Unmarshal(env.Data, &run); err != nil {
				continue
			}
			// Live Activities follow every scan event; only a failure notifies.
			s.followScan(ctx, env.Event, run)
			if env.Event != "scan.failed" {
				continue
			}
			s.noticeScanFailed(ctx, run.LibraryID, adminReason(run.ErrorMessage, "The scan did not finish"), time.Now())
		}
	}
}

func (s *AdminPushService) noticeScanFailed(ctx context.Context, libraryID int, reason string, at time.Time) {
	notice := ScanFailedNotice(strconv.Itoa(libraryID), s.libraryName(ctx, libraryID), reason, at, adminScanWindow)
	if err := s.Notify(ctx, notice); err != nil {
		s.logger.WarnContext(ctx, "admin push for a failed scan failed", "library_id", libraryID, "error", err)
	}
}

// ---- Polled state ---------------------------------------------------------

// AdminBackupRun is the latest backup run, for the failure watcher.
type AdminBackupRun struct {
	ID         string
	Status     string
	Error      string
	FinishedAt time.Time
}

// AdminPluginHealth is one installed plugin's state, for the failure watcher.
type AdminPluginHealth struct {
	InstallationID string
	Name           string
	Failed         bool
	Reason         string
}

// AdminPushSources are the state readers the watchers poll. A nil reader
// switches that watcher off: a server without backups has nothing to report.
type AdminPushSources struct {
	// LastBackup reports the latest backup run; ok is false when none exists.
	LastBackup func(context.Context) (run AdminBackupRun, ok bool, err error)
	// Plugins lists installed plugins with their runtime state.
	Plugins func(context.Context) ([]AdminPluginHealth, error)
	// ActiveBackup reports the backup running now, or nil, for Live Activities.
	ActiveBackup func(context.Context) (*AdminActiveBackup, error)
}

// outageTracker reports each outage once, after it has lasted the grace
// period, and forgets it when the subject recovers.
type outageTracker struct {
	mu    sync.Mutex
	since map[string]time.Time
	fired map[string]bool
}

func newOutageTracker() *outageTracker {
	return &outageTracker{since: map[string]time.Time{}, fired: map[string]bool{}}
}

// observe records the subject's state. It returns the outage start and true
// the first time the outage has lasted grace.
func (t *outageTracker) observe(id string, down bool, now time.Time, grace time.Duration) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !down {
		delete(t.since, id)
		delete(t.fired, id)
		return time.Time{}, false
	}
	start, ok := t.since[id]
	if !ok {
		t.since[id] = now
		start = now
	}
	if t.fired[id] || now.Sub(start) < grace {
		return start, false
	}
	t.fired[id] = true
	return start, true
}

// bucket rounds an outage start so replicas that saw it a few seconds apart
// still derive one delivery id.
func (t *outageTracker) bucket(start time.Time) time.Time {
	return start.Truncate(adminOutageBucket)
}

// reported tells whether the subject's current outage has already fired.
func (t *outageTracker) reported(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fired[id]
}

// RunWatchers polls backup, node, plugin and mount state until ctx ends. Every
// replica may run it.
func (s *AdminPushService) RunWatchers(ctx context.Context, sources AdminPushSources) {
	if !s.Available() {
		return
	}
	nodes, plugins, nodeStates, backupStates := newOutageTracker(), newOutageTracker(), newStateTracker(), newStateTracker()
	startedAt := s.now()
	lastWarning := startedAt
	tick := time.NewTicker(adminWatchEvery)
	defer tick.Stop()
	for {
		s.watchOnce(ctx, sources, nodes, plugins, nodeStates, backupStates, &lastWarning)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *AdminPushService) watchOnce(ctx context.Context, sources AdminPushSources, nodes, plugins *outageTracker, nodeStates, backupStates *stateTracker, lastWarning *time.Time) {
	now := s.now()
	if sources.LastBackup != nil {
		s.watchBackup(ctx, sources.LastBackup, now, backupStates)
	}
	s.watchLiveBackup(ctx, sources.ActiveBackup, sources.LastBackup)
	s.watchNodes(ctx, nodes, nodeStates, now)
	if sources.Plugins != nil {
		s.watchPlugins(ctx, sources.Plugins, plugins, now)
	}
	s.watchMounts(ctx, lastWarning)
}

// watchBackup reads the latest backup run. states remembers the runs seen, so the health widget is
// woken once per change; nil skips the wake.
func (s *AdminPushService) watchBackup(ctx context.Context, last func(context.Context) (AdminBackupRun, bool, error), now time.Time, states *stateTracker) {
	run, ok, err := last(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "admin push could not read backup status", "error", err)
		}
		return
	}
	if !ok {
		return
	}
	failed := run.Status == "failed" || run.Status == "interrupted"
	// A run that is old news on a restart is not a new failure.
	fresh := now.Sub(run.FinishedAt) <= adminBackupFreshness
	if states != nil {
		// The health widget shows whether the latest run failed: a new failed run changes it, and so
		// does a success after a failure, however old that failure was.
		_, runSeen := states.record("backup:"+run.ID, !failed)
		latestSucceeded, latestSeen := states.record("backup", !failed)
		if (failed && !runSeen && fresh) || (!failed && latestSeen && !latestSucceeded) {
			s.wakeHealth(ctx, "backup", run.ID)
		}
	}
	if !failed || !fresh {
		return
	}
	reason := adminReason(run.Error, "The backup did not finish")
	if err := s.Notify(ctx, BackupFailedNotice(run.ID, s.ServerName(ctx), reason)); err != nil {
		s.logger.WarnContext(ctx, "admin push for a failed backup failed", "error", err)
	}
}

// stateTracker reports when a subject's up or down state changes. The first sight of a
// subject only records it: a server that starts with a node already down changes nothing.
type stateTracker struct {
	mu   sync.Mutex
	last map[string]bool
}

func newStateTracker() *stateTracker { return &stateTracker{last: map[string]bool{}} }

func (t *stateTracker) changed(id string, up bool) bool {
	previous, seen := t.record(id, up)
	return seen && previous != up
}

// record stores the subject's state and returns the one it had before, if it was seen.
func (t *stateTracker) record(id string, up bool) (previous, seen bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	previous, seen = t.last[id]
	t.last[id] = up
	return previous, seen
}

// wakeHealth wakes the health widget (Needs a look). The watchers call it before they notify, so
// the alert, which refreshes the widgets of every device it reaches, covers the wake for those
// devices; a device with that alert switched off still gets the wake.
func (s *AdminPushService) wakeHealth(ctx context.Context, subject, id string) {
	if err := s.Wake(ctx, AdminWidgetHealth, 0); err != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "admin widget wake for health failed", "subject", subject, "id", id, "error", err)
	}
}

func (s *AdminPushService) watchNodes(ctx context.Context, tracker *outageTracker, states *stateTracker, now time.Time) {
	rows, err := s.store.pool.Query(ctx, `SELECT id::text, name, healthy FROM stream_nodes WHERE enabled`)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "admin push could not read nodes", "error", err)
		}
		return
	}
	type node struct {
		id, name string
		healthy  bool
	}
	var nodes []node
	for rows.Next() {
		var n node
		if err := rows.Scan(&n.id, &n.name, &n.healthy); err == nil {
			nodes = append(nodes, n)
		}
	}
	rows.Close()
	for _, n := range nodes {
		// A node going offline or coming back changes what the nodes widget shows, at once.
		if states != nil && states.changed(n.id, n.healthy) {
			if err := s.Wake(ctx, AdminWidgetNodes, 0); err != nil {
				s.logger.WarnContext(ctx, "admin widget wake for a node failed", "node_id", n.id, "error", err)
			}
		}
		if start, fire := tracker.observe(n.id, !n.healthy, now, adminNodeGrace); fire {
			if err := s.Notify(ctx, NodeOfflineNotice(n.id, n.name, tracker.bucket(start))); err != nil {
				s.logger.WarnContext(ctx, "admin push for an offline node failed", "node_id", n.id, "error", err)
			}
		}
	}
}

func (s *AdminPushService) watchPlugins(ctx context.Context, list func(context.Context) ([]AdminPluginHealth, error), tracker *outageTracker, now time.Time) {
	plugins, err := list(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "admin push could not read plugins", "error", err)
		}
		return
	}
	for _, p := range plugins {
		wasReported := tracker.reported(p.InstallationID)
		start, fire := tracker.observe(p.InstallationID, p.Failed, now, 0)
		// A plugin failing, or recovering after its failure was reported, changes the health widget.
		if fire || (wasReported && !p.Failed) {
			s.wakeHealth(ctx, "plugin", p.InstallationID)
		}
		if fire {
			reason := adminReason(p.Reason, "The plugin stopped")
			if err := s.Notify(ctx, PluginFailedNotice(p.InstallationID, p.Name, reason, tracker.bucket(start))); err != nil {
				s.logger.WarnContext(ctx, "admin push for a failed plugin failed", "plugin", p.InstallationID, "error", err)
			}
		}
	}
}

// watchMounts reports a library whose storage a scan found unreachable. The
// scanner records that as a "dead_root" warning on the folder; nothing probes a
// mount between scans, so this is as fresh as the last scan.
func (s *AdminPushService) watchMounts(ctx context.Context, since *time.Time) {
	rows, err := s.store.pool.Query(ctx, `SELECT id, COALESCE(scan_warning_message, ''), scan_warning_at FROM media_folders
		WHERE enabled AND scan_warning_code = 'dead_root' AND scan_warning_at > $1 ORDER BY scan_warning_at`, *since)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "admin push could not read library warnings", "error", err)
		}
		return
	}
	type warning struct {
		id      int
		message string
		at      time.Time
	}
	var warnings []warning
	for rows.Next() {
		var w warning
		if err := rows.Scan(&w.id, &w.message, &w.at); err == nil {
			warnings = append(warnings, w)
		}
	}
	rows.Close()
	for _, w := range warnings {
		s.noticeScanFailed(ctx, w.id, adminReason(w.message, "The library's storage can't be reached"), w.at)
		if w.at.After(*since) {
			*since = w.at
		}
	}
}
