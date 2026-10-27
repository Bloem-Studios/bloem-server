package apiv2

import (
	"net/http"

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
// The routes are served by chi in the native client group, mounted only when
// the deployment has notifications; bloemClientDocumentOp declares that
// group's gates.
//
// The bodies reuse notifications.DeliveryRowPayload, which is already exported
// and already feeds the client DTO registry, so the row itself is not restated
// and cannot drift. The page envelopes are restated, because the handler owns
// them unexported; TestBloemNotificationDocumentMatchesTheServedShape holds
// those in step.

// BloemNotificationPage is one page of the native inbox.
type BloemNotificationPage struct {
	Items []notifications.DeliveryRowPayload `json:"items" doc:"Inbox rows, newest first. Empty, never null."`
	Page  BloemNotificationPageInfo          `json:"page"`
	// ReadCutoff is the boundary the page was taken against. The next-page
	// cursor carries it, so paging stays stable while new rows arrive.
	ReadCutoff string `json:"read_cutoff" doc:"Opaque boundary this traversal was taken against. page.next_cursor already carries it."`
}

// BloemNotificationPageInfo is the page block, spelled the way every other v2
// collection spells it.
type BloemNotificationPageInfo struct {
	NextCursor string `json:"next_cursor,omitempty" doc:"Inbox: cursor for the next page, absent on the last page. Sync: the position to resume from, present whenever the page has rows or a cursor was sent."`
	HasMore    bool   `json:"has_more" doc:"Whether another page exists. Always false on an initial sync snapshot."`
}

// BloemNotificationSyncPage is one forward-sync page: the rows newer than the
// client's cursor, plus the unread total it puts on a badge.
type BloemNotificationSyncPage struct {
	Items       []notifications.DeliveryRowPayload `json:"items" doc:"Rows newer than the cursor, oldest first. Empty, never null."`
	Page        BloemNotificationPageInfo          `json:"page"`
	UnreadCount int                                `json:"unread_count" doc:"Unread rows across the whole inbox, not just this page."`
	// InitialSnapshot separates a first sync from a replayed gap: a client that
	// cannot tell them apart either re-notifies the viewer about its own
	// backlog or silently drops the rows it missed while offline.
	InitialSnapshot bool `json:"initial_snapshot" doc:"True when no cursor was sent, so this page is the newest snapshot rather than a replayed gap."`
}

// BloemNotificationListInput pages the inbox.
type BloemNotificationListInput struct {
	Status string `query:"status" enum:"all,unread" doc:"unread lists only unread rows; any other value lists all rows." required:"false"`
	Limit  int    `query:"limit" default:"25" doc:"Maximum rows to return. Values above 100 are treated as 100; a missing, non-numeric or non-positive value uses the default." required:"false"`
	Cursor string `query:"cursor" doc:"Opaque cursor from page.next_cursor of a previous inbox page; an invalid cursor answers 400." required:"false"`
}

// BloemNotificationListOutput is one inbox page.
type BloemNotificationListOutput struct {
	Body BloemNotificationPage
}

// BloemNotificationSyncInput resumes a forward sync.
type BloemNotificationSyncInput struct {
	Limit  int    `query:"limit" default:"50" doc:"Maximum rows to return. Values above 100 are treated as 100; a missing, non-numeric or non-positive value uses the default." required:"false"`
	Cursor string `query:"cursor" doc:"Opaque cursor from page.next_cursor of a previous sync; omit for the newest snapshot. Inbox cursors are not accepted here. An invalid cursor answers 400." required:"false"`
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
	{
		op := bloemClientDocumentOp(reg, "GET", "/notifications", "listBloemNotifications", "notifications",
			"The acting profile's inbox, newest first, with the alert fields a row needs to render.", http.StatusOK)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing or cursor is invalid.",
			500: "internal_error: the viewer's access could not be resolved, or the inbox could not be read.",
		})
		registerBloemChiDocument[BloemNotificationListInput, BloemNotificationListOutput](reg, op)
	}

	{
		op := bloemClientDocumentOp(reg, "GET", "/notifications/sync", "syncBloemNotifications", "notifications",
			"Rows newer than a cursor, for a client waking from a push or reconnecting across a gap.", http.StatusOK)
		op.Description = "Without a cursor, returns the newest rows up to limit, oldest first, with has_more false. With a cursor, returns the rows after it and has_more reports whether more remain; an empty page returns the sent cursor as next_cursor."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing or cursor is invalid.",
			500: "internal_error: the viewer's access could not be resolved, or the inbox could not be read.",
		})
		registerBloemChiDocument[BloemNotificationSyncInput, BloemNotificationSyncOutput](reg, op)
	}

	{
		op := bloemClientDocumentOp(reg, "GET", "/notifications/{id}", "getBloemNotification", "notifications",
			"One inbox row. Another profile's row is absent, not forbidden.", http.StatusOK)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			404: "not_found: no row with this ID belongs to the selected profile, or X-Profile-Id names no profile of this account.",
			500: "internal_error: the viewer's access could not be resolved, or the row could not be read.",
		})
		registerBloemChiDocument[BloemNotificationItemInput, BloemNotificationItemOutput](reg, op)
	}
}
