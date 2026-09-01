package notifications

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
)

// OperationalDispatch describes how one operational delivery (a non-fanout
// notice such as webhook.auto_disabled or request.fulfilled) reaches the
// per-target channels. WebhookFilter selects which of the profile's enabled
// webhooks receive it; nil means the type must not reach webhooks at all
// (e.g. the auto-disable notice's loop guard). Web push has no per-type
// filter: profile-level gating happens before dispatch.
type OperationalDispatch struct {
	WebhookFilter func(Webhook) bool
}

// DispatchOperational durably creates one operational delivery. The inbox row
// and the per-target webhook / web push / Apple push outbox rows commit in a
// single transaction — a crash afterwards delays channel sends instead of dropping
// them, because the retry workers recover pending outbox rows — then realtime
// and channel dispatch run post-commit. Returns nil when the delivery deduped
// away (the partial unique indexes make operational notices idempotent).
func (s *System) DispatchOperational(ctx context.Context, delivery Delivery, opts OperationalDispatch) (*InsertedDelivery, error) {
	inserted, err := s.DispatchOperationalBatch(ctx, []Delivery{delivery}, opts)
	if err != nil || len(inserted) == 0 {
		return nil, err
	}
	return &inserted[0], nil
}

// DispatchOperationalBatch is DispatchOperational for many recipients at
// once (admin announcements, S-1): every inbox row and every outbox attempt
// for the whole batch commits in ONE transaction, then each inserted row is
// dispatched post-commit exactly like a single operational delivery. Rows
// that dedupe away are skipped; the returned set is what was inserted.
func (s *System) DispatchOperationalBatch(ctx context.Context, deliveries []Delivery, opts OperationalDispatch) ([]InsertedDelivery, error) {
	return s.dispatchOperationalBatch(ctx, deliveries, opts, nil)
}

// dispatchOperationalBatch is the shared implementation; prepare (optional)
// runs inside the transaction before the inbox insert so callers can commit
// their own parent row (an announcement) atomically with the fanout.
func (s *System) dispatchOperationalBatch(ctx context.Context, deliveries []Delivery, opts OperationalDispatch, prepare func(context.Context, pgx.Tx) error) ([]InsertedDelivery, error) {
	if s == nil || len(deliveries) == 0 {
		return nil, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin operational dispatch tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if prepare != nil {
		if err := prepare(ctx, tx); err != nil {
			return nil, err
		}
	}
	inserted, err := s.Deliveries.BulkInsert(ctx, tx, deliveries)
	if err != nil {
		return nil, err
	}
	if len(inserted) == 0 {
		if prepare == nil {
			return nil, nil
		}
		// Everything deduped away, but the caller's parent row still counts.
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit operational dispatch: %w", err)
		}
		return nil, nil
	}
	profileIDs := make([]string, 0, len(inserted))
	seen := make(map[string]struct{}, len(inserted))
	for _, row := range inserted {
		if _, ok := seen[row.ProfileID]; ok {
			continue
		}
		seen[row.ProfileID] = struct{}{}
		profileIDs = append(profileIDs, row.ProfileID)
	}

	if opts.WebhookFilter != nil && s.webhookRepo != nil && s.Settings.WebhooksEnabled(ctx) {
		hooksByProfile, err := s.webhookRepo.ListEnabledByProfiles(ctx, tx, profileIDs)
		if err != nil {
			return nil, err
		}
		attempts := make([]DeliveryAttempt, 0, len(inserted))
		for _, row := range inserted {
			for _, hook := range hooksByProfile[row.ProfileID] {
				if !opts.WebhookFilter(hook) {
					continue
				}
				attempts = append(attempts, DeliveryAttempt{
					ID:                     ulid.Make().String(),
					NotificationDeliveryID: row.ID,
					TargetID:               hook.ID,
				})
			}
		}
		if err := s.webhookRepo.EnqueueAttempts(ctx, tx, attempts); err != nil {
			return nil, err
		}
	}
	if s.webPushRepo != nil && s.Settings.WebPushEnabled(ctx) {
		subsByProfile, err := s.webPushRepo.ListEnabledByProfiles(ctx, tx, profileIDs)
		if err != nil {
			return nil, err
		}
		attempts := make([]DeliveryAttempt, 0, len(inserted))
		for _, row := range inserted {
			for _, sub := range subsByProfile[row.ProfileID] {
				attempts = append(attempts, DeliveryAttempt{
					ID:                     ulid.Make().String(),
					NotificationDeliveryID: row.ID,
					TargetID:               sub.ID,
				})
			}
		}
		if err := s.webPushRepo.EnqueueAttempts(ctx, tx, attempts); err != nil {
			return nil, err
		}
	}
	if s.pushDeviceRepo != nil {
		if platforms := s.Settings.EnabledPushPlatforms(ctx); len(platforms) > 0 {
			devicesByProfile, err := s.pushDeviceRepo.ListEnabledPushByProfiles(ctx, tx, profileIDs, platforms)
			if err != nil {
				return nil, err
			}
			attempts := make([]PushDeliveryAttempt, 0, len(inserted))
			for _, row := range inserted {
				attempts = append(attempts, newPushDeliveryAttempts(row.ID, devicesByProfile[row.ProfileID])...)
			}
			if err := s.pushDeviceRepo.EnqueuePushAttempts(ctx, tx, attempts); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit operational dispatch: %w", err)
	}

	// Post-commit dispatch is best-effort: the durable inbox rows cover
	// websocket reconnect, and the retry workers recover the outbox rows.
	ids := make([]string, 0, len(inserted))
	for _, row := range inserted {
		ids = append(ids, row.ID)
	}
	full, err := s.Deliveries.GetRowsByIDs(ctx, ids)
	if err != nil {
		s.logger.WarnContext(ctx, "operational delivery reload failed",
			"delivery_count", len(ids), "error", err)
		return inserted, nil
	}
	for i := range full {
		if err := s.dispatcher.Dispatch(ctx, full[i]); err != nil {
			s.logger.WarnContext(ctx, "operational delivery dispatch failed",
				"delivery_id", full[i].ID, "error", err)
		}
	}
	return inserted, nil
}
