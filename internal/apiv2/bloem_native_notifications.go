package apiv2

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

// The notification inbox on Bloem's native surface.
//
// Silo's /api/v2 inbox exists and works. What it cannot do is describe a Bloem
// alert: its NotificationItem projects the media fields of a delivery row and
// has no place for the headline, body, severity, deeplink, call to action,
// expiry or dismissal that a system.alert or system.announcement row carries
// (docs/specs/client-engagement.md §A). A client reading the v2 inbox can list
// an alert it has no way to render.
//
// Adding those fields to the v2 projection would mean editing a Silo-owned
// file for a Bloem-only concept -- Silo has no alert model at all -- so the
// native surface serves the same rows whole instead. Both surfaces read the
// same rows through the same service methods, so they cannot drift into
// serving different inboxes.
//
// The bodies reuse notifications.DeliveryRowPayload, which is already exported
// and already feeds the client DTO registry, so the row itself is not restated
// and cannot drift. The page envelopes are restated, because the handler owns
// them unexported; TestBloemNotificationDocumentMatchesTheServedShape holds
// those in step.

// BloemNotificationPage is one page of the native inbox.
type BloemNotificationPage struct {
	Items []notifications.DeliveryRowPayload `json:"items" doc:"Inbox rows, newest first."`
	Page  BloemNotificationPageInfo          `json:"page"`
	// ReadCutoff is the boundary the page was taken against. A client sends it
	// back with the next request so paging stays stable while new rows arrive,
	// and compares it against a row's created_at to decide whether the row
	// renders as unread.
	ReadCutoff string `json:"read_cutoff" doc:"Opaque boundary this traversal was taken against."`
}

// BloemNotificationPageInfo is the page block, spelled the way every other v2
// collection spells it.
type BloemNotificationPageInfo struct {
	NextCursor string `json:"next_cursor,omitempty" doc:"Cursor for the next page; absent on the last one."`
	HasMore    bool   `json:"has_more" doc:"Whether another page exists."`
}

// BloemNotificationSyncPage is one forward-sync page: the rows newer than the
// client's cursor, plus the unread total it puts on a badge.
type BloemNotificationSyncPage struct {
	Items       []notifications.DeliveryRowPayload `json:"items" doc:"Rows newer than the cursor, oldest first."`
	Page        BloemNotificationPageInfo          `json:"page"`
	UnreadCount int                                `json:"unread_count" doc:"Unread rows across the whole inbox, not just this page."`
	// InitialSnapshot separates a first sync from a replayed gap: a client that
	// cannot tell them apart either re-notifies the viewer about its own
	// backlog or silently drops the rows it missed while offline.
	InitialSnapshot bool `json:"initial_snapshot" doc:"True when no cursor was sent, so this page is the newest snapshot rather than a replayed gap."`
}

// BloemNotificationListInput pages the inbox.
type BloemNotificationListInput struct {
	Status string `query:"status" enum:"all,unread" doc:"Filter to unread rows." required:"false"`
	Limit  int    `query:"limit" doc:"Maximum rows to return." required:"false"`
	Cursor string `query:"cursor" doc:"Opaque cursor from page.next_cursor." required:"false"`
}

// BloemNotificationListOutput is one inbox page.
type BloemNotificationListOutput struct {
	Body BloemNotificationPage
}

// BloemNotificationSyncInput resumes a forward sync.
type BloemNotificationSyncInput struct {
	Limit  int    `query:"limit" doc:"Maximum rows to return." required:"false"`
	Cursor string `query:"cursor" doc:"Opaque cursor from a previous sync; omit for the newest snapshot." required:"false"`
}

// BloemNotificationSyncOutput is one forward-sync page.
type BloemNotificationSyncOutput struct {
	Body BloemNotificationSyncPage
}

// BloemNotificationItemInput names one row. Delivery IDs are ULIDs minted by
// the notification store, so the identity is text rather than a UUID.
type BloemNotificationItemInput struct {
	ID string `path:"id" minLength:"1" doc:"Delivery identity."`
}

// BloemNotificationItemOutput is one inbox row.
type BloemNotificationItemOutput struct {
	Body notifications.DeliveryRowPayload
}

func registerBloemNotifications(reg *Registry) {
	Register(reg, Operation{
		Operation: bloemOp("GET", "/notifications", "listBloemNotifications", "notifications",
			"The acting profile's inbox, newest first, with the alert fields a row needs to render."),
		Class: ClassProfileScoped,
	}, func(context.Context, *BloemNotificationListInput) (*BloemNotificationListOutput, error) {
		return &BloemNotificationListOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("GET", "/notifications/sync", "syncBloemNotifications", "notifications",
			"Rows newer than a cursor, for a client waking from a push or reconnecting across a gap."),
		Class: ClassProfileScoped,
	}, func(context.Context, *BloemNotificationSyncInput) (*BloemNotificationSyncOutput, error) {
		return &BloemNotificationSyncOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("GET", "/notifications/{id}", "getBloemNotification", "notifications",
			"One inbox row. Another profile's row is absent, not forbidden."),
		Class: ClassProfileScoped,
	}, func(context.Context, *BloemNotificationItemInput) (*BloemNotificationItemOutput, error) {
		return &BloemNotificationItemOutput{}, nil
	})
}
