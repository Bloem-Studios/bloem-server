package apiv2

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

type EventsCapabilityService interface {
	EventsCapability() handlers.EventsCapability
}

type EventsCapabilities struct {
	SchemaVersion               int      `json:"schema_version"`
	SubscribeFrame              bool     `json:"subscribe_frame"`
	DeclaredChannels            bool     `json:"declared_channels"`
	SubscribeGracePeriodSeconds int      `json:"subscribe_grace_period_seconds"`
	MaxRequestedChannels        int      `json:"max_requested_channels"`
	Channels                    []string `json:"channels"`
}
type EventsCapabilitiesOutput struct{ Body EventsCapabilities }

func registerEventsCapability(reg *Registry) {
	op := Operation{Operation: humaOp(http.MethodGet, Prefix+"/events/capabilities", "getEventsCapabilities", "realtime", "Read realtime subscription channels and limits."), Class: ClassAuthenticated, ServiceBacked: true}
	Register(reg, op, func(_ context.Context, _ *struct{}) (*EventsCapabilitiesOutput, error) {
		if reg.deps.EventsCapability == nil {
			return nil, unavailable("realtime events")
		}
		value := reg.deps.EventsCapability.EventsCapability()
		out := EventsCapabilities{SchemaVersion: value.SchemaVersion, SubscribeFrame: value.SubscribeFrame, DeclaredChannels: value.DeclaredChannels, SubscribeGracePeriodSeconds: value.SubscribeGracePeriodSeconds, MaxRequestedChannels: value.MaxRequestedChannels, Channels: make([]string, 0, len(value.Channels))}
		for _, channel := range value.Channels {
			out.Channels = append(out.Channels, string(channel))
		}
		return &EventsCapabilitiesOutput{Body: out}, nil
	})
}
