package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
)

// announcementListLimit bounds the admin list; announcements are rare.
const announcementListLimit = 200

// AnnouncementService composes admin announcements (S-1): validates the
// body, resolves the audience, fans out through the operational dispatch
// path in one transaction, and withdraws.
type AnnouncementService struct {
	system     *System
	repo       *AnnouncementRepository
	recipients recipientSource
	now        func() time.Time
}

func newAnnouncementService(system *System, repo *AnnouncementRepository) *AnnouncementService {
	return &AnnouncementService{
		system:     system,
		repo:       repo,
		recipients: systemRecipientSource{system: system},
		now:        time.Now,
	}
}

// Create validates, resolves recipients, and dispatches. The announcement
// row, every inbox row, and every outbox attempt commit together; realtime
// notification.created events follow per recipient.
func (s *AnnouncementService) Create(ctx context.Context, createdBy int, in AnnouncementInput) (*Announcement, error) {
	if s == nil {
		return nil, ErrAnnouncementInvalid
	}
	now := s.now().UTC()
	switch in.Type {
	case "":
		in.Type = DeliveryTypeSystemAnnouncement
	case DeliveryTypeSystemAlert, DeliveryTypeSystemAnnouncement:
	default:
		return nil, fmt.Errorf("%w: type must be system.alert or system.announcement", ErrAnnouncementInvalid)
	}
	body, err := NormalizeAlertBody(in.Body, now)
	if err != nil {
		return nil, err
	}
	targeting, err := validateTargeting(in.Targeting)
	if err != nil {
		return nil, err
	}
	recipients, err := resolveAnnouncementRecipients(ctx, s.recipients, targeting)
	if err != nil {
		return nil, err
	}
	// Informational announcements honor the profile's master inbox toggle;
	// operational alerts always land (an outage notice is not opt-out).
	if in.Type == DeliveryTypeSystemAnnouncement {
		recipients, err = s.filterOptedOut(ctx, recipients)
		if err != nil {
			return nil, err
		}
	}
	if len(recipients) == 0 {
		return nil, ErrAnnouncementNoRecipients
	}

	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode alert body: %w", err)
	}
	announcement := Announcement{
		ID:             ulid.Make().String(),
		Type:           in.Type,
		Body:           body,
		Targeting:      targeting,
		RecipientCount: len(recipients),
		CreatedAt:      now,
	}
	if createdBy > 0 {
		announcement.CreatedBy = &createdBy
	}
	deliveries := make([]Delivery, 0, len(recipients))
	for _, recipient := range recipients {
		announcementID := announcement.ID
		deliveries = append(deliveries, Delivery{
			ID:             ulid.Make().String(),
			UserID:         recipient.UserID,
			ProfileID:      recipient.ProfileID,
			Type:           in.Type,
			ReasonFlags:    []byte("{}"),
			Body:           rawBody,
			ExpiresAt:      body.ExpiresAt,
			AnnouncementID: &announcementID,
		})
	}
	_, err = s.system.dispatchOperationalBatch(ctx, deliveries, OperationalDispatch{
		WebhookFilter: func(Webhook) bool { return true },
	}, func(ctx context.Context, tx pgx.Tx) error {
		return s.repo.Insert(ctx, tx, announcement)
	})
	if err != nil {
		return nil, err
	}
	return &announcement, nil
}

func (s *AnnouncementService) filterOptedOut(ctx context.Context, recipients []AnnouncementRecipient) ([]AnnouncementRecipient, error) {
	if s.system == nil || s.system.Preferences == nil || s.system.pool == nil || len(recipients) == 0 {
		return recipients, nil
	}
	profileIDs := make([]string, 0, len(recipients))
	for _, r := range recipients {
		profileIDs = append(profileIDs, r.ProfileID)
	}
	tx, err := s.system.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin preferences read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	prefs, err := s.system.Preferences.GetMany(ctx, tx, profileIDs)
	if err != nil {
		return nil, err
	}
	out := recipients[:0]
	for _, r := range recipients {
		if p, ok := prefs[r.ProfileID]; ok && !p.Enabled {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// List returns recent announcements, withdrawn ones included.
func (s *AnnouncementService) List(ctx context.Context) ([]Announcement, error) {
	if s == nil {
		return nil, nil
	}
	return s.repo.List(ctx, announcementListLimit)
}

// Withdraw stops an announcement: unread inbox rows are deleted (their
// pending outbox attempts cascade away, canceling undelivered pushes) and
// read rows are expired so no feed shows them again; each affected profile
// gets a notification.withdrawn realtime event. Idempotent.
func (s *AnnouncementService) Withdraw(ctx context.Context, id string) error {
	if s == nil {
		return ErrAnnouncementNotFound
	}
	tx, err := s.system.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin withdraw tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	found, err := s.repo.MarkWithdrawn(ctx, tx, id, s.now().UTC())
	if err != nil {
		return err
	}
	if !found {
		return ErrAnnouncementNotFound
	}
	withdrawn, err := s.system.Deliveries.WithdrawAnnouncement(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit withdraw: %w", err)
	}
	if s.system.hub != nil {
		for _, row := range withdrawn {
			_ = s.system.hub.PublishJSON(ctx, evt.ChannelNotifications, EventNotificationWithdrawn,
				map[string]any{"id": row.ID, "profile_id": row.ProfileID},
				evt.PublishOptions{UserID: row.UserID, ProfileID: row.ProfileID})
		}
	}
	return nil
}
