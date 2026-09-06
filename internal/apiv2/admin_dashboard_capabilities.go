package apiv2

import "context"

// AdminDashboardCapabilities describes support in this API build, not the
// health or configuration of the services behind the individual operations.
type AdminDashboardCapabilities struct {
	ServerLayouts    bool `json:"server_layouts"`
	Timeseries       bool `json:"timeseries"`
	PlaybackActivity bool `json:"playback_activity"`
	TopActivity      bool `json:"top_activity"`
	Health           bool `json:"health"`
	LogLevelList     bool `json:"log_level_list"`
	WatchProviders   bool `json:"watch_providers"`
	DownloadsStats   bool `json:"downloads_stats"`
}
type AdminDashboardCapabilitiesOutput struct{ Body AdminDashboardCapabilities }

func registerAdminDashboardCapabilities(reg *Registry) {
	op := Operation{Operation: humaOp("GET", Prefix+"/admin/dashboard/capabilities", "getAdminDashboardCapabilities", "admin-observability", "Discover dashboard features supported by v2 in this build. Support does not promise dependency readiness; layout storage and multi-level log filtering are not yet available on v2."), Class: ClassActingAdmin, DemoRestricted: true}
	Register(reg, op, func(context.Context, *struct{}) (*AdminDashboardCapabilitiesOutput, error) {
		return &AdminDashboardCapabilitiesOutput{Body: AdminDashboardCapabilities{
			Timeseries: true, PlaybackActivity: true, TopActivity: true, Health: true, WatchProviders: true, DownloadsStats: true,
		}}, nil
	})
}
