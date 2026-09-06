package handlers

import (
	"context"
	"errors"
	"maps"
	"slices"
)

var ErrAdminSettingsUnavailable = errors.New("administrator settings unavailable")

// InspectAdminSettings shares runtime defaults and secret redaction with the
// bridge handlers. Clone before redacting: stores may return a cached map.
func (h *AdminHandler) InspectAdminSettings(ctx context.Context, effective bool) (map[string]string, error) {
	if h.SettingsRepo == nil {
		return nil, ErrAdminSettingsUnavailable
	}
	values, err := h.SettingsRepo.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	values = maps.Clone(values)
	if effective {
		values = h.effectiveAdminSettings(values)
	}
	if values == nil {
		values = map[string]string{}
	}
	redactAdminSettings(values)
	return values, nil
}

type AdminSensitiveSettingsStatus struct {
	Configured   []string
	ManagedByEnv []string
}

func (h *AdminHandler) InspectAdminSensitiveSettings(ctx context.Context) (AdminSensitiveSettingsStatus, error) {
	if h.SettingsRepo == nil {
		return AdminSensitiveSettingsStatus{}, ErrAdminSettingsUnavailable
	}
	all, err := h.SettingsRepo.GetAll(ctx)
	if err != nil {
		return AdminSensitiveSettingsStatus{}, err
	}
	return h.adminSensitiveSettingsStatus(all), nil
}

func (h *AdminHandler) adminSensitiveSettingsStatus(all map[string]string) AdminSensitiveSettingsStatus {
	configured := map[string]struct{}{}
	for key := range sensitiveSettingKeys {
		if all[key] != "" || h.BootstrapSensitiveConfigured[key] || h.BootstrapSensitiveValues[key] != "" {
			configured[key] = struct{}{}
		}
	}
	out := AdminSensitiveSettingsStatus{Configured: slices.Sorted(maps.Keys(configured)), ManagedByEnv: []string{}}
	if out.Configured == nil {
		out.Configured = []string{}
	}
	for key, present := range h.BootstrapSensitiveConfigured {
		if present {
			out.ManagedByEnv = append(out.ManagedByEnv, key)
		}
	}
	slices.Sort(out.ManagedByEnv)
	return out
}
