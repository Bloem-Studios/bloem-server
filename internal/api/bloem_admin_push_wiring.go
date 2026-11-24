package api

import (
	"context"
	"log/slog"

	"github.com/Silo-Server/silo-server/internal/backup"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// Bloem admin push wiring (docs/architecture/bloem-admin-push.md). Everything
// the admin push needs from the rest of the server is read through the
// functions below, so no Silo-owned package changes for it.

// bloemAdminPushLifecycle decorates the request lifecycle notifier so a request
// that waits for an administrator also pushes to admin devices. Without the
// service, or without a request repository, it returns next unchanged.
func bloemAdminPushLifecycle(next requests.LifecycleNotifier, deps Dependencies, repo *requests.Repository) requests.LifecycleNotifier {
	push := deps.Notifications.BloemAdminPush()
	if push == nil {
		return next
	}
	var tenants *tenancy.Store
	if deps.DB != nil {
		tenants = tenancy.NewStore(deps.DB)
	}
	return notifications.NewAdminPushRequestNotifier(next, push, func(ctx context.Context, requesterUserID int) int {
		if repo == nil {
			return 1
		}
		// Administrators act inside the requester's organization, so that is
		// the queue "N waiting" counts.
		if tenants != nil && requesterUserID > 0 {
			if organizationID, err := tenants.AccountOrganization(ctx, requesterUserID); err == nil {
				if counts, err := repo.CountAdminViewsInOrganization(ctx, organizationID); err == nil {
					return counts.NeedsApproval
				}
			}
		}
		if counts, err := repo.CountAdminViews(ctx); err == nil {
			return counts.NeedsApproval
		}
		return 1
	})
}

// ConfigureBloemAdminPush names the server in notification copy and, on a
// process that runs workers, starts the sender and the watchers. It is called
// once, after every dependency exists and before the router is built. The
// goroutines end with AppContext.
func ConfigureBloemAdminPush(deps *Dependencies, workers bool) {
	if deps == nil {
		return
	}
	// Stream insights sends its limit alerts through admin push, and runs
	// on every process with a database (docs/architecture/bloem-stream-insights.md).
	configureBloemStreamInsights(deps)
	push := deps.Notifications.BloemAdminPush()
	if push == nil {
		return
	}
	if deps.BrandingService != nil {
		branding := deps.BrandingService
		push.SetServerName(func(ctx context.Context) string { return branding.Load(ctx).ServerName })
	}
	if !workers || deps.AppContext == nil {
		return
	}
	ctx := deps.AppContext
	sources := notifications.AdminPushSources{}
	if deps.DB != nil {
		status := backup.PostgresStatus{Pool: deps.DB}
		sources.LastBackup = func(ctx context.Context) (notifications.AdminBackupRun, bool, error) {
			record, err := status.Read(ctx)
			if err != nil || record.Last == nil {
				return notifications.AdminBackupRun{}, false, err
			}
			last := record.Last
			finished := last.FinishedAt
			if finished.IsZero() {
				// An interrupted run never wrote a finish time.
				finished = last.StartedAt
			}
			return notifications.AdminBackupRun{ID: last.ID, Status: last.Status, Error: last.Error, FinishedAt: finished}, true, nil
		}
		// The backup running now, for the admin apps' Live Activities.
		sources.ActiveBackup = func(ctx context.Context) (*notifications.AdminActiveBackup, error) {
			record, err := status.Read(ctx)
			if err != nil || record.Active == nil || record.Active.Operation != backup.OperationBackup {
				return nil, err
			}
			active := record.Active
			return &notifications.AdminActiveBackup{ID: active.BackupID, StartedAt: active.StartedAt, Progress: active.Progress}, nil
		}
	}
	if deps.DB != nil && deps.PluginService != nil {
		installations := plugins.NewInstallationStore(deps.DB)
		service := deps.PluginService
		sources.Plugins = func(ctx context.Context) ([]notifications.AdminPluginHealth, error) {
			rows, err := installations.List(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]notifications.AdminPluginHealth, 0, len(rows))
			for _, installation := range rows {
				if installation == nil || !installation.Enabled {
					continue
				}
				state := service.RuntimeStateOf(installation)
				out = append(out, notifications.AdminPluginHealth{
					InstallationID: itoa(installation.ID), Name: installation.PluginID,
					Failed: state.State == plugins.ResidentFailed, Reason: state.LastError,
				})
			}
			return out, nil
		}
	}
	go push.Run(ctx)
	go push.WatchEvents(ctx, deps.EventsHub)
	go push.RunWatchers(ctx, sources)
	slog.Info("bloem admin push started")
}
