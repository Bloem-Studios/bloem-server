package notifications

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/requests"
)

// BookRequestNotifier reuses operational inbox, push, email, webhooks and
// server-channel settings. Request IDs are UUIDs, distinct from Silo IDs.
type BookRequestNotifier struct{ system *System }

func NewBookRequestNotifier(system *System) *BookRequestNotifier {
	if system == nil {
		return nil
	}
	return &BookRequestNotifier{system: system}
}
func bookNotificationRequest(r bookrequests.Request) requests.Request {
	return requests.Request{ID: r.ID, Title: r.Title, MediaType: requests.MediaType(r.MediaType), Status: requests.Status(r.Status), Outcome: requests.Outcome(r.Outcome), OutcomeReason: r.OutcomeReason, RequestedByUserID: r.RequestedByUserID, RequestedByProfileID: r.RequestedByProfileID, LibraryContentID: r.LibraryContentID}
}
func (n *BookRequestNotifier) Lifecycle(ctx context.Context, r bookrequests.Request, event string) {
	if n == nil || n.system == nil {
		return
	}
	adapter := NewRequestLifecycleNotifier(n.system)
	req := bookNotificationRequest(r)
	switch event {
	case "submitted":
		adapter.RequestSubmitted(ctx, req)
	case "auto_approved":
		adapter.RequestApproved(ctx, req, requests.ApprovalOriginPolicy)
	case "approve":
		adapter.RequestApproved(ctx, req, requests.ApprovalOriginAdmin)
	case "decline":
		adapter.RequestDeclined(ctx, req)
	}
}
func (n *BookRequestNotifier) Fulfilled(ctx context.Context, r bookrequests.Request) error {
	if n == nil || n.system == nil {
		return nil
	}
	req := bookNotificationRequest(r)
	if r.LibraryContentID != "" {
		return NewRequestFulfillmentNotifier(n.system).NotifyFulfilled(ctx, req, r.LibraryContentID)
	}
	// Manual completion is a status update, not a claim that a catalog item is
	// playable. The generic operational delivery carries the requested title.
	req.OutcomeReason = "Marked fulfilled by an administrator"
	return n.system.dispatchRequestLifecycle(ctx, req, DeliveryTypeRequestFulfilled)
}
func (n *BookRequestNotifier) AnnounceFulfilled(ctx context.Context, r bookrequests.Request) {
	if n == nil || n.system == nil {
		return
	}
	NewRequestFulfillmentNotifier(n.system).AnnounceFulfilled(ctx, bookNotificationRequest(r))
}
