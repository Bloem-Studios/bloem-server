package handlers

import (
	"net/http"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/notifications"

	"github.com/go-chi/chi/v5"
)

// The notification inbox on Bloem's native surface.
//
// Bloem's notification rows carry alert fields Silo has no concept of: a
// headline, body text, a severity, a deeplink, a call to action, an expiry and
// a per-viewer dismissal (docs/specs/client-engagement.md §A). They are how a
// system.alert or system.announcement row renders at all -- the media fields
// describe what a row points at, while these are what a row actually says.
//
// Silo's /api/v2 inbox projects its own NotificationItem, which has no place to
// put any of them. That projection is Silo's and stays Silo's: a client reading
// the v2 inbox can list an alert it has no way to display. The native surface
// serves the same rows whole instead.
//
// Nothing here re-implements the inbox. Paging, the read cutoff, the unread
// count and the profile scope all come from the same NotificationsHandler
// service methods the other surfaces call, so the three cannot drift into
// serving different rows for the same request. Only the projection differs --
// and the native projection is the absence of one, since
// notifications.DeliveryRowPayload is already the whole row.
//
// The envelope is v2's, not v1's: `items` with a `page`, because a client that
// already speaks v2 should not have to learn a second set of conventions to
// speak Bloem's own surface.

// bloemNotificationPage is one page of the native inbox.
type bloemNotificationPage struct {
	Items []notifications.DeliveryRowPayload `json:"items"`
	Page  bloemNotificationPageInfo          `json:"page"`
	// ReadCutoff is the boundary this page was taken against. A client sends
	// it back with the next request so paging stays stable while new rows
	// arrive, and compares it against a row's created_at to decide whether the
	// row renders as unread.
	ReadCutoff string `json:"read_cutoff"`
}

// bloemNotificationSyncPage is one forward-sync page: the rows newer than the
// client's cursor, plus the unread total it displays on a badge.
type bloemNotificationSyncPage struct {
	Items       []notifications.DeliveryRowPayload `json:"items"`
	Page        bloemNotificationPageInfo          `json:"page"`
	UnreadCount int                                `json:"unread_count"`
	// InitialSnapshot is true when the client sent no cursor, so this page is
	// the bounded newest snapshot rather than a gap being replayed.
	InitialSnapshot bool `json:"initial_snapshot"`
}

// bloemNotificationPageInfo is the v2 page block: an opaque cursor for the next
// request, and whether one is worth making.
type bloemNotificationPageInfo struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// bloemInboxCursor packs the two positions a stable inbox page needs into one
// opaque token: where the page resumes, and the cutoff the whole traversal was
// taken against. They travel together because a client that round-trips only
// one of them silently pages through a shifting list.
//
// The encoding is the delivery cursor's own, twice, joined by a separator
// neither half can contain: both halves are base64url, which has no "~".
type bloemInboxCursor struct {
	Before  notifications.Cursor
	Through notifications.Cursor
}

const bloemInboxCursorSeparator = "~"

func (c bloemInboxCursor) encode() string {
	return c.Before.Encode() + bloemInboxCursorSeparator + c.Through.Encode()
}

func decodeBloemInboxCursor(raw string) (bloemInboxCursor, bool) {
	before, through, found := strings.Cut(raw, bloemInboxCursorSeparator)
	if !found {
		return bloemInboxCursor{}, false
	}
	b, err := notifications.DecodeCursor(before)
	if err != nil {
		return bloemInboxCursor{}, false
	}
	t, err := notifications.DecodeCursor(through)
	if err != nil {
		return bloemInboxCursor{}, false
	}
	return bloemInboxCursor{Before: b, Through: t}, true
}

// HandleBloemNotificationList handles GET /api/bloem/v1/notifications.
func (h *NotificationsHandler) HandleBloemNotificationList(w http.ResponseWriter, r *http.Request) {
	profileID := apimw.GetProfileID(r.Context())
	limit := parseNotificationsLimit(r, notificationsDefaultLimit)

	var before, through *notifications.Cursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		pos, ok := decodeBloemInboxCursor(raw)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid cursor")
			return
		}
		before, through = &pos.Before, &pos.Through
	}

	view, err := h.ListNotificationInbox(r.Context(), profileID, r.URL.Query().Get("status") == "unread", limit, before, through)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list notifications")
		return
	}

	writeJSON(w, http.StatusOK, bloemInboxPageOf(view))
}

// bloemInboxPageOf shapes one service view into the wire page. It is separate
// from the handler so the shaping -- which is the whole of what this surface
// adds -- can be tested without a database behind it.
func bloemInboxPageOf(view NotificationInboxPageView) bloemNotificationPage {
	page := bloemNotificationPage{
		// An absent list and an empty one are the same inbox, and a client
		// that has to handle both spellings will eventually handle one wrong.
		Items:      []notifications.DeliveryRowPayload{},
		ReadCutoff: view.Through.Encode(),
		Page:       bloemNotificationPageInfo{HasMore: view.More},
	}
	if len(view.Items) > 0 {
		page.Items = view.Items
	}
	if view.More && len(view.Items) > 0 {
		last := view.Items[len(view.Items)-1]
		page.Page.NextCursor = bloemInboxCursor{
			Before:  notifications.Cursor{CreatedAt: last.CreatedAt, ID: last.ID},
			Through: view.Through,
		}.encode()
	}
	return page
}

// HandleBloemNotificationSync handles GET /api/bloem/v1/notifications/sync: the
// forward, ascending traversal a client runs after waking from a push or
// reconnecting across a gap.
func (h *NotificationsHandler) HandleBloemNotificationSync(w http.ResponseWriter, r *http.Request) {
	profileID := apimw.GetProfileID(r.Context())
	limit := parseNotificationsLimit(r, notificationsSyncLimit)

	var since *notifications.Cursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := notifications.DecodeCursor(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid cursor")
			return
		}
		since = &cursor
	}

	rows, more, unread, err := h.SyncNotificationInbox(r.Context(), profileID, limit, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to sync notifications")
		return
	}

	writeJSON(w, http.StatusOK, bloemSyncPageOf(rows, more, unread, since))
}

// bloemSyncPageOf shapes one forward-sync result into the wire page.
func bloemSyncPageOf(rows []notifications.DeliveryRowPayload, more bool, unread int, since *notifications.Cursor) bloemNotificationSyncPage {
	page := bloemNotificationSyncPage{
		Items:           []notifications.DeliveryRowPayload{},
		UnreadCount:     unread,
		InitialSnapshot: since == nil,
		Page:            bloemNotificationPageInfo{HasMore: more},
	}
	if len(rows) > 0 {
		page.Items = rows
		last := rows[len(rows)-1]
		page.Page.NextCursor = notifications.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	} else if since != nil {
		// Nothing new: hand the client back the cursor it sent, so an empty
		// page does not cost it its place in the stream.
		page.Page.NextCursor = since.Encode()
	}
	return page
}

// HandleBloemNotificationGet handles GET /api/bloem/v1/notifications/{id}.
// Another profile's row is a 404, not a 403: whether it exists is not this
// viewer's business.
func (h *NotificationsHandler) HandleBloemNotificationGet(w http.ResponseWriter, r *http.Request) {
	row, err := h.GetNotificationInboxItem(r.Context(), apimw.GetProfileID(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}
