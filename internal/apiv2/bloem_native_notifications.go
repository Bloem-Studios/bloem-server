package apiv2

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

// The notification inbox on Bloem's native surface.
//
// Silo's /api/v2 inbox is Silo's: on a Bloem server it serves a Silo app the
// inbox a Silo server would, which leaves Bloem's own notices (system.alert,
// system.announcement) out (handlers.BloemSiloInboxView). Its NotificationItem
// also has no place for the headline, body, severity, deeplink, call to
// action, expiry or dismissal a notice carries
// (docs/architecture/admin-announcements.md).
//
// The native surface is where Bloem's clients read every row, notices
// included, whole. It reads through Silo's own inbox service methods, so the
// two surfaces differ only by the notices.
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

// BloemNotificationCount is the unread total.
type BloemNotificationCount struct {
	Count int `json:"count" doc:"Unread rows in the whole inbox, Bloem's own notices included."`
}

// BloemNotificationCountOutput is the unread total.
type BloemNotificationCountOutput struct {
	Body BloemNotificationCount
}

// BloemNotificationReadAll names the boundary a read-all covers.
type BloemNotificationReadAll struct {
	Through string `json:"through" minLength:"1" doc:"read_cutoff of the inbox page the viewer saw. Rows that arrived after it stay unread."`
}

// BloemNotificationReadAllInput marks the inbox read up to a cutoff.
type BloemNotificationReadAllInput struct {
	Body BloemNotificationReadAll
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

	{
		op := bloemClientDocumentOp(reg, "GET", "/notifications/unread-count", "getBloemNotificationUnreadCount", "notifications",
			"How many inbox rows are unread, Bloem's own notices included.", http.StatusOK)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing.",
			500: "internal_error: the viewer's access could not be resolved, or the inbox could not be read.",
		})
		registerBloemChiDocument[struct{}, BloemNotificationCountOutput](reg, op)
	}

	{
		op := bloemClientDocumentOp(reg, "POST", "/notifications/{id}/read", "markBloemNotificationRead", "notifications",
			"Mark one inbox row read. Repeating it is not an error.", http.StatusNoContent)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			404: "not_found: no row with this ID belongs to the selected profile, or X-Profile-Id names no profile of this account.",
			500: "internal_error: the viewer's access could not be resolved, or the row could not be updated.",
		})
		registerBloemChiDocument[BloemNotificationItemInput, struct{}](reg, op)
	}

	{
		op := bloemClientDocumentOp(reg, "POST", "/notifications/read-all", "markBloemNotificationsRead", "notifications",
			"Mark every inbox row up to a page's read_cutoff read, Bloem's own notices included.", http.StatusNoContent)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing, or through is missing or not a read_cutoff.",
			500: "internal_error: the viewer's access could not be resolved, or the inbox could not be updated.",
		})
		registerBloemChiDocument[BloemNotificationReadAllInput, struct{}](reg, op)
	}
}
