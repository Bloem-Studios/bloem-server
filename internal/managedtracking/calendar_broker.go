package managedtracking

import (
	"context"

	calendar "github.com/Silo-Server/silo-server/internal/managedtracking/calendarwire"
)

type calendarBroker struct {
	calendar.UnimplementedCalendarHostServer
	service      *Service
	installation int
}

func (b *calendarBroker) Read(ctx context.Context, r *calendar.CalendarRequest) (*calendar.CalendarPage, error) {
	out, err := b.service.ReadCalendar(ctx, b.installation, r)
	return out, safeRPC(err)
}
func (b *calendarBroker) CheckAuthority(ctx context.Context, r *calendar.CalendarAuthorityRequest) (*calendar.CalendarAuthorityResponse, error) {
	out, err := b.service.CheckCalendarAuthority(ctx, b.installation, r)
	return out, safeRPC(err)
}
