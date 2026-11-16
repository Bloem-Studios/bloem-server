package notifications

import (
	"sync"

	evt "github.com/Silo-Server/silo-server/internal/events"
)

// bloemSystem holds Bloem's additions to System, embedded so the Silo struct
// carries one line instead of a field per addition. Exported fields are
// promoted (System.Announcements).
type bloemSystem struct {
	// Announcements composes admin alerts/announcements (S-1).
	Announcements *AnnouncementService

	scopes ScopeResolver
	hub    *evt.Hub

	// adminPush is built on first use by BloemAdminPush.
	adminPushOnce sync.Once
	adminPush     *AdminPushService
}

// Hub is the events hub the system was built with, or nil.
func (s *System) Hub() *evt.Hub {
	if s == nil {
		return nil
	}
	return s.hub
}
