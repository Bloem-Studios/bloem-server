package apiv2

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/jellycompat"
)

type AdminJellyfinCompatSettingsService interface {
	UpdateAdminJellyfinCompatSettings(context.Context, handlers.AdminJellyfinCompatSettingsPatch, func(handlers.AdminSettingsSnapshot) error) (jellycompat.WebComponentStatus, error)
}
type AdminJellyfinCompatSettingsInput struct {
	RawBody     []byte
	IfMatch     string `header:"If-Match"`
	IfNoneMatch string `header:"If-None-Match"`
	Body        handlers.AdminJellyfinCompatSettingsPatch
}
type AdminJellyfinCompatSettingsOutput struct {
	ETag string `header:"ETag"`
	Body AdminJellyfinCompatStatus
}

func registerAdminJellyfinCompatSettings(reg *Registry) {
	op := Operation{Operation: humaOp("PATCH", Prefix+"/admin/jellyfin-compat/settings", "updateAdminJellyfinCompatSettings", "admin-settings", "Apply compatibility settings under the canonical settings guard. Runtime notifications are not replay receipts; no web asset installation or listener restart is started."), Class: ClassActingAdmin, DemoRestricted: true, ServiceBacked: true, Guarded: true, RetrySafety: RetrySafetyNonRetryable}
	op.MaxBodyBytes = 1 << 20
	Register(reg, op, func(ctx context.Context, in *AdminJellyfinCompatSettingsInput) (*AdminJellyfinCompatSettingsOutput, error) {
		if reg.deps.AdminJellyfinCompatSettings == nil {
			return nil, unavailable("Jellyfin compatibility settings")
		}
		if p := rejectNonNullableNulls(in.RawBody, nil); p != nil {
			return nil, p
		}
		result, err := reg.deps.AdminJellyfinCompatSettings.UpdateAdminJellyfinCompatSettings(ctx, in.Body, reg.settingsWriteGuard(ctx, in.IfMatch, in.IfNoneMatch))
		if err != nil {
			return nil, adminSettingsWriteProblem(err)
		}
		return &AdminJellyfinCompatSettingsOutput{Body: adminJellyfinCompatStatusOf(result)}, nil
	})
}
