package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/google/uuid"
)

type bookNotificationBackend interface {
	bookRequestRecipients(context.Context, bookrequests.Request, bool) ([]requests.Follower, error)
	dispatchBookRequestNotice(context.Context, bookrequests.Request, requests.Follower, string) error
}

// BookRequestNotifier reuses operational inbox, push, email and webhook channels.
// Tenant request titles never go to deployment-wide server-channel broadcasts.
type BookRequestNotifier struct{ backend bookNotificationBackend }

func NewBookRequestNotifier(system *System) *BookRequestNotifier {
	if system == nil {
		return nil
	}
	return &BookRequestNotifier{backend: system}
}
func bookNotificationRequest(r bookrequests.Request) requests.Request {
	return requests.Request{ID: r.ID, Title: r.Title, MediaType: requests.MediaType(r.MediaType), Status: requests.Status(r.Status), Outcome: requests.Outcome(r.Outcome), OutcomeReason: r.OutcomeReason, RequestedByUserID: r.RequestedByUserID, RequestedByProfileID: r.RequestedByProfileID, LibraryContentID: r.LibraryContentID}
}
func (n *BookRequestNotifier) Lifecycle(ctx context.Context, r bookrequests.Request, event string) {
	if event != "submitted" && event != "approve" && event != "decline" {
		return
	}
	dispatchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	go func() {
		defer cancel()
		if err := n.notify(dispatchCtx, r, event); err != nil {
			slog.WarnContext(dispatchCtx, "audiobook request notice failed", "request_id", r.ID, "event", event, "error", err)
		}
	}()
}
func (n *BookRequestNotifier) Fulfilled(ctx context.Context, r bookrequests.Request) error {
	return n.notify(ctx, r, "fulfilled")
}
func (n *BookRequestNotifier) notify(ctx context.Context, r bookrequests.Request, event string) error {
	if n == nil || n.backend == nil {
		return nil
	}
	recipients, err := n.backend.bookRequestRecipients(ctx, r, event == "submitted")
	if err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err = n.backend.dispatchBookRequestNotice(ctx, r, recipient, event); err != nil {
			return err
		}
	}
	return nil
}

// Recipients are re-resolved before every notice. A removed membership, moved
// profile, disabled account or suspended organization cannot receive old work.
func (s *System) bookRequestRecipients(ctx context.Context, r bookrequests.Request, admins bool) ([]requests.Follower, error) {
	rows, err := s.pool.Query(ctx, `
 SELECT p.user_id,p.id FROM user_profiles p
 JOIN users u ON u.id=p.user_id AND u.enabled
 JOIN organization_memberships m ON m.account_id=p.user_id AND m.organization_id=p.organization_id AND m.status='active'
 JOIN organizations o ON o.id=p.organization_id AND (o.status='active' OR (o.is_default AND o.status='initializing'))
 WHERE p.organization_id=$1 AND (($2 AND m.legacy_role='admin') OR (NOT $2 AND p.user_id=$3 AND p.id=$4))
 ORDER BY p.user_id,p.id`, r.OrganizationID, admins, r.RequestedByUserID, r.RequestedByProfileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recipients := []requests.Follower{}
	for rows.Next() {
		var recipient requests.Follower
		if err = rows.Scan(&recipient.UserID, &recipient.ProfileID); err != nil {
			return nil, err
		}
		recipients = append(recipients, recipient)
	}
	return recipients, rows.Err()
}
func (s *System) dispatchBookRequestNotice(ctx context.Context, r bookrequests.Request, recipient requests.Follower, event string) error {
	req := bookNotificationRequest(r)
	req.RequestedByUserID, req.RequestedByProfileID = recipient.UserID, recipient.ProfileID
	if event == "fulfilled" {
		if r.LibraryContentID != "" {
			return NewRequestFulfillmentNotifier(s).NotifyFulfilled(ctx, req, r.LibraryContentID)
		}
	}
	prefs, err := s.Preferences.Get(ctx, recipient.ProfileID)
	if err != nil {
		return err
	}
	if !prefs.Enabled {
		return nil
	}
	body := AlertBody{Title: "Audiobook request fulfilled", Body: fmt.Sprintf("An administrator marked your request for %s fulfilled.", r.Title), Deeplink: "/requests?kind=audiobook", Severity: SeverityInfo, Dismissible: true}
	switch event {
	case "submitted":
		body.Title = "New audiobook request"
		body.Body = fmt.Sprintf("%s was requested in your organization.", r.Title)
		body.Deeplink = "/admin/requests?kind=audiobook"
	case "approve":
		body.Title = "Audiobook request approved"
		body.Body = fmt.Sprintf("Your request for %s was approved and is awaiting fulfillment.", r.Title)
	case "decline":
		body.Title = "Audiobook request declined"
		body.Body = fmt.Sprintf("Your request for %s was declined.", r.Title)
		if r.OutcomeReason != "" {
			body.Body += " " + r.OutcomeReason
		}
	case "fulfilled":
	default:
		return nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	// Stable delivery IDs give manual/submitted notices the same retry safety
	// that request.* notices obtain from their partial unique indexes.
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("bloem-book-request:%s:%s:%d:%s", r.ID, event, recipient.UserID, recipient.ProfileID))).String()
	_, err = s.DispatchOperational(ctx, Delivery{ID: id, UserID: recipient.UserID, ProfileID: recipient.ProfileID, Type: DeliveryTypeSystemAlert, Body: raw}, OperationalDispatch{WebhookFilter: func(hook Webhook) bool { return hook.NotifyRequests }})
	return err
}
