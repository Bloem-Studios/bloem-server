package notifications

import (
	"github.com/Silo-Server/silo-server/internal/auth"
	evt "github.com/Silo-Server/silo-server/internal/events"
)

// bloemSystem holds Bloem's additions to System, embedded so the Silo struct
// carries one line instead of a field per addition. Exported fields are
// promoted (System.AudienceTickets, System.Announcements).
type bloemSystem struct {
	// AudienceTickets replaces Silo's Tickets: the shared auth ticket
	// authority used by every websocket audience.
	AudienceTickets auth.AudienceTicketStore
	// Announcements composes admin alerts/announcements (S-1).
	Announcements *AnnouncementService

	scopes ScopeResolver
	hub    *evt.Hub
}
