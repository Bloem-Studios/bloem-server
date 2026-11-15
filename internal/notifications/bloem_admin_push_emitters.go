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

// RequestSubmitted forwards to the wrapped notifier, then pushes when the
// request is pending. An auto-approved request waits for nobody and is skipped.
func (n *adminPushRequestNotifier) RequestSubmitted(ctx context.Context, req requests.Request) {
	if n.LifecycleNotifier != nil {
		n.LifecycleNotifier.RequestSubmitted(ctx, req)
	}
	if req.Status != requests.StatusPending {
		return
	}
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	go func() {
		defer cancel()
		if n.done != nil {
			defer n.done()
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

// WatchScans notifies admins when a library scan fails.
func (s *AdminPushService) WatchScans(ctx context.Context, hub *evt.Hub) {
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
			if env.Channel != evt.ChannelScans || env.Event != "scan.failed" {
				continue
			}
			var run evt.ScanRun
			if err := json.Unmarshal(env.Data, &run); err != nil {
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

// RunWatchers polls backup, node, plugin and mount state until ctx ends. Every
// replica may run it.
func (s *AdminPushService) RunWatchers(ctx context.Context, sources AdminPushSources) {
	if !s.Available() {
		return
	}
	nodes, plugins := newOutageTracker(), newOutageTracker()
	startedAt := s.now()
	lastWarning := startedAt
	tick := time.NewTicker(adminWatchEvery)
	defer tick.Stop()
	for {
		s.watchOnce(ctx, sources, nodes, plugins, &lastWarning)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *AdminPushService) watchOnce(ctx context.Context, sources AdminPushSources, nodes, plugins *outageTracker, lastWarning *time.Time) {
	now := s.now()
	if sources.LastBackup != nil {
		s.watchBackup(ctx, sources.LastBackup, now)
	}
	s.watchNodes(ctx, nodes, now)
	if sources.Plugins != nil {
		s.watchPlugins(ctx, sources.Plugins, plugins, now)
	}
	s.watchMounts(ctx, lastWarning)
}

func (s *AdminPushService) watchBackup(ctx context.Context, last func(context.Context) (AdminBackupRun, bool, error), now time.Time) {
	run, ok, err := last(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "admin push could not read backup status", "error", err)
		}
		return
	}
	// A run that is old news on a restart is not a new failure.
	if !ok || (run.Status != "failed" && run.Status != "interrupted") || now.Sub(run.FinishedAt) > adminBackupFreshness {
		return
	}
	reason := adminReason(run.Error, "The backup did not finish")
	if err := s.Notify(ctx, BackupFailedNotice(run.ID, s.ServerName(ctx), reason)); err != nil {
		s.logger.WarnContext(ctx, "admin push for a failed backup failed", "error", err)
	}
}

func (s *AdminPushService) watchNodes(ctx context.Context, tracker *outageTracker, now time.Time) {
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
		if start, fire := tracker.observe(p.InstallationID, p.Failed, now, 0); fire {
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
