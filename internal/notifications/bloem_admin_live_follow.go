package notifications

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	evt "github.com/Silo-Server/silo-server/internal/events"
)

// Following the jobs admin Live Activities show (docs/architecture/bloem-admin-push.md):
// a scan through its scan.* events on the events hub, which every replica hears, and a
// backup through the status the push watchers poll each minute. Each turns the job into
// an adminJobState for the activities that show it; the sender paces and orders the
// sends in the database, so every replica may follow the same job.

const (
	// adminLiveCadence is the least time between two updates of one activity; a start,
	// an end and a change of phase go at once.
	adminLiveCadence = 30 * time.Second
	// adminLiveScanStartAfter is how long a scan runs before the server push-starts it.
	adminLiveScanStartAfter = time.Minute
	// adminLiveBackupEndWindow lets a backup's end reach activities registered shortly
	// after the run finished (a push-started one registers its token a moment later).
	adminLiveBackupEndWindow = 2 * time.Minute
)

// AdminActiveBackup is the backup running now, for the Live Activity follower.
type AdminActiveBackup struct {
	ID        string
	StartedAt time.Time
	// Progress is the percentage done, 0 to 100.
	Progress float64
}

func fraction(done, total float64) *float64 {
	if total <= 0 {
		return nil
	}
	f := min(max(done/total, 0), 1)
	return &f
}

// scanLiveState turns a scan event into the state and event its activities get.
func scanLiveState(event string, run evt.ScanRun, now time.Time) (adminJobState, string, bool) {
	state := adminJobState{LibraryID: strconv.Itoa(run.LibraryID), UpdatedAt: now.Unix()}
	liveEvent := LiveEventUpdate
	switch event {
	case "scan.started", "scan.progress":
		state.Phase = LivePhaseRunning
	case "scan.completed", "scan.cancelled":
		state.Phase, liveEvent = LivePhaseFinished, LiveEventEnd
	case "scan.failed":
		state.Phase, liveEvent = LivePhaseFailed, LiveEventEnd
	default:
		return state, "", false
	}
	if r := run.Result; r != nil && r.TotalFiles > 0 {
		processed, total := min(max(r.FilesProcessed, 0), r.TotalFiles), r.TotalFiles
		state.Processed, state.Total = &processed, &total
		state.Progress = fraction(float64(processed), float64(total))
	}
	return state, liveEvent, true
}

// followScan keeps the activities of a library's scan current, and push-starts the
// scan on opted-in devices once it has run a minute.
func (s *AdminPushService) followScan(ctx context.Context, event string, run evt.ScanRun) {
	if run.LibraryID <= 0 || !s.LiveActivitiesEnabled(ctx) {
		return
	}
	now := s.now()
	state, liveEvent, ok := scanLiveState(event, run, now)
	if !ok {
		return
	}
	library := strconv.Itoa(run.LibraryID)
	if err := s.queueLive(ctx, liveUpdate{Kind: LiveKindScan, SubjectID: library, Event: liveEvent, State: state}); err != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "admin live activity update for a scan failed", "library_id", run.LibraryID, "error", err)
	}
	if liveEvent == LiveEventUpdate && run.StartedAt != nil && run.ID != "" && now.Sub(*run.StartedAt) >= adminLiveScanStartAfter {
		s.pushStartLive(ctx, LiveKindScan, library, "scan:"+run.ID, AdminKindScanFailed, state, *run.StartedAt)
	}
}

// watchLiveBackup follows the backup that is running, or ends its activities with the
// latest run's outcome once none is.
func (s *AdminPushService) watchLiveBackup(ctx context.Context, active func(context.Context) (*AdminActiveBackup, error), last func(context.Context) (AdminBackupRun, bool, error)) {
	if active == nil || !s.LiveActivitiesEnabled(ctx) {
		return
	}
	running, err := active(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "admin live activities could not read the running backup", "error", err)
		}
		return
	}
	var run AdminBackupRun
	var ok bool
	if running == nil && last != nil {
		if run, ok, err = last(ctx); err != nil {
			return
		}
	}
	s.followBackup(ctx, running, run, ok)
}

// followBackup keeps backup activities current: running while a backup runs, pushed
// to opted-in devices at once, and ended with the latest run's outcome afterwards.
func (s *AdminPushService) followBackup(ctx context.Context, running *AdminActiveBackup, last AdminBackupRun, lastOK bool) {
	if !s.LiveActivitiesEnabled(ctx) {
		return
	}
	now := s.now()
	state := adminJobState{Phase: LivePhaseRunning, UpdatedAt: now.Unix()}
	if running != nil {
		state.Progress = fraction(running.Progress, 100)
		if err := s.queueLive(ctx, liveUpdate{Kind: LiveKindBackup, Event: LiveEventUpdate, State: state}); err != nil && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "admin live activity update for a backup failed", "error", err)
		}
		key := running.ID
		if key == "" {
			key = strconv.FormatInt(running.StartedAt.Unix(), 10)
		}
		s.pushStartLive(ctx, LiveKindBackup, "", "backup:"+key, AdminKindBackupFailed, state, running.StartedAt)
		return
	}
	if !lastOK || last.FinishedAt.IsZero() {
		return
	}
	state.Phase = LivePhaseFinished
	if last.Status == "failed" || last.Status == "interrupted" {
		state.Phase = LivePhaseFailed
	}
	end := liveUpdate{Kind: LiveKindBackup, Event: LiveEventEnd, State: state, CreatedBefore: last.FinishedAt.Add(adminLiveBackupEndWindow)}
	if err := s.queueLive(ctx, end); err != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "admin live activity end for a backup failed", "error", err)
	}
}

// pushStartLive starts the job's activity on every device that allowed the server to
// start this kind, has the matching alert switched on, and does not show the job
// already. Each device is started once per job, across replicas.
func (s *AdminPushService) pushStartLive(ctx context.Context, kind, subject, jobKey, alertKind string, state adminJobState, startedAt time.Time) {
	if !s.liveModeAvailable() || !s.settings.ApplePushDeliveryEnabled(ctx) {
		return
	}
	devices, err := s.store.liveStartDevices(ctx, kind, subject, jobKey, alertKind, s.now())
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "admin live activity start candidates failed", "kind", kind, "error", err)
		}
		return
	}
	for i := range devices {
		claimed, err := s.store.claimLiveStart(ctx, devices[i].ID, jobKey)
		if err != nil || !claimed {
			continue
		}
		// One try per job and device: a start that fails is not retried, so a relay in
		// trouble is not asked again on every progress event.
		s.sendLiveStart(ctx, &devices[i], kind, state, startedAt)
	}
}

// liveStartDevices lists the devices a job may be push-started on.
func (s *adminPushStore) liveStartDevices(ctx context.Context, kind, subject, jobKey, alertKind string, now time.Time) ([]AdminPushDevice, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+adminDeviceColumns+` FROM bloem_admin_push_devices d
		WHERE d.enabled AND d.platform = 'apple' AND d.live_start_token_ciphertext IS NOT NULL AND $1 = ANY(d.live_start_kinds)
			AND NOT ($4 = ANY(d.disabled_kinds)) AND `+adminAccountLive+` AND `+adminLoginLive+`
			AND NOT EXISTS (SELECT 1 FROM bloem_admin_live_activities a WHERE a.device_id = d.id AND a.kind = $1
				AND a.subject_id IS NOT DISTINCT FROM NULLIF($2, '') AND a.ended_at IS NULL AND a.expires_at > $5)
			AND NOT EXISTS (SELECT 1 FROM bloem_admin_live_starts st WHERE st.device_id = d.id AND st.job_key = $3)
		ORDER BY d.id`, kind, subject, jobKey, alertKind, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminPushDevice
	for rows.Next() {
		d, err := scanAdminDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// claimLiveStart records that the job is being started on the device; only the first
// replica to ask gets true.
func (s *adminPushStore) claimLiveStart(ctx context.Context, deviceID, jobKey string) (bool, error) {
	var claimed string
	err := s.pool.QueryRow(ctx, `INSERT INTO bloem_admin_live_starts (device_id, job_key) VALUES ($1, $2)
		ON CONFLICT DO NOTHING RETURNING job_key`, deviceID, jobKey).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// adminLiveKeep is how long an activity row outlives its lifetime before the pruner
// deletes it, and a start claim outlives its job.
const adminLiveKeep = 4 * time.Hour

// pruneLive runs with the hourly push purge. It deletes activities that ended and were
// sent their end, activities 4 hours past their lifetime, and the activities and
// push-to-start token of a device whose login ended or whose account is no longer an
// admin; and start claims older than a job's lifetime.
func (s *AdminPushService) pruneLive(ctx context.Context) {
	if err := s.store.pruneLive(ctx, s.now()); err != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "prune admin live activities failed", "error", err)
	}
}

func (s *adminPushStore) pruneLive(ctx context.Context, now time.Time) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM bloem_admin_live_activities a
		WHERE (a.ended_at IS NOT NULL AND a.pending_state IS NULL) OR a.expires_at < $1::timestamptz - $2::interval
			OR EXISTS (SELECT 1 FROM bloem_admin_push_devices d WHERE d.id = a.device_id
				AND NOT (`+adminAccountLive+` AND `+adminLoginLive+`))`, now, adminLiveKeep); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE bloem_admin_push_devices d SET live_start_token_ciphertext = NULL, live_start_kinds = '{}'
		WHERE d.live_start_token_ciphertext IS NOT NULL AND NOT (`+adminAccountLive+` AND `+adminLoginLive+`)`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM bloem_admin_live_starts WHERE created_at < $1::timestamptz - $2::interval`,
		now, adminLiveLifetime+adminLiveKeep)
	return err
}
