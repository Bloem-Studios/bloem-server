package notifications

import (
	"context"
)

// DispatchOperationalBatch is DispatchOperational for many recipients at
// once (admin announcements, S-1): every inbox row and every outbox attempt
// for the whole batch commits in ONE transaction, then each inserted row is
// dispatched post-commit exactly like a single operational delivery. Rows
// that dedupe away are skipped; the returned set is what was inserted.
func (s *System) DispatchOperationalBatch(ctx context.Context, deliveries []Delivery, opts OperationalDispatch) ([]InsertedDelivery, error) {
	return s.dispatchOperationalBatch(ctx, deliveries, opts, nil)
}
