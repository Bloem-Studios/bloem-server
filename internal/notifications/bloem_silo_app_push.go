package notifications

import (
	"context"
	"fmt"
)

// Bloem's own notices (system.alert and system.announcement: announcements,
// upgrade prompts, book request notices, saved campaigns) never wake a
// Silo-app phone.
//
// push_devices holds the phones of Silo's apps, which a Bloem server wakes
// through Silo's relay exactly as a Silo server does
// (docs/architecture/bloem-push-relays.md). A woken Silo app reads the
// delivery from Silo's notification routes, whose item has no place for an
// alert's title or message, so it can only show the type's first word and
// "Open Silo to view it". A Silo server never sends such a notice. Bloem's
// own surfaces (the inbox, web push, webhooks, the native routes) still get
// every row.

// sendsToSiloAppPush reports whether a delivery of this type may be pushed to
// a Silo-app device.
func sendsToSiloAppPush(deliveryType string) bool {
	return !IsSystemDeliveryType(deliveryType)
}

// DispatchBloemOperational is DispatchOperational for Bloem's own delivery
// types: the same inbox row, webhook, web push and plugin destination
// attempts in one transaction, then realtime, but no push to Silo-app devices.
// Bloem's notices name no catalog item, so a delivery with a SeriesID is
// refused rather than resolved against the recipient's access.
func (s *System) DispatchBloemOperational(ctx context.Context, delivery Delivery, opts OperationalDispatch) (*InsertedDelivery, error) {
	if s == nil {
		return nil, nil
	}
	if !IsSystemDeliveryType(delivery.Type) {
		return nil, fmt.Errorf("bloem operational delivery %s has Silo type %q", delivery.ID, delivery.Type)
	}
	if delivery.SeriesID != nil && *delivery.SeriesID != "" {
		return nil, fmt.Errorf("bloem operational delivery %s names a catalog item", delivery.ID)
	}
	channels := s.operationalChannels(ctx)
	channels.pushPlatforms = nil
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin operational dispatch tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row, err := s.insertOperational(ctx, tx, delivery, opts, nil, channels)
	if err != nil || row == nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit operational dispatch: %w", err)
	}
	s.dispatchOperationalRow(ctx, *row)
	return row, nil
}
