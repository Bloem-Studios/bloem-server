package notifications

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

// WithProfileLifecycleTransaction preserves the profile lifecycle capability
// across the decorator. Embedding userstore.UserStore promotes only that
// interface's methods, so without this forward the wrapper silently answers
// "no" to the handler's capability probe and every profile create/update/delete
// fails preflight with a 503 -- which is exactly what it did in production.
//
// Only the Postgres backend can run these in a caller-owned pgx transaction, so
// a backend that cannot returns ErrProfileLifecycleUnsupported and the handler
// maps it to the same unavailable response the probe used to produce.
func (s *interestTrackingStore) WithProfileLifecycleTransaction(
	ctx context.Context,
	tx pgx.Tx,
	fn func(userstore.ProfileLifecycleWriter) error,
) error {
	transactioner, ok := s.UserStore.(userstore.ProfileLifecycleTransactioner)
	if !ok {
		return userstore.ErrProfileLifecycleUnsupported
	}
	return transactioner.WithProfileLifecycleTransaction(ctx, tx, fn)
}

var _ userstore.ProfileLifecycleTransactioner = (*interestTrackingStore)(nil)
var _ userstore.ProfileLifecycleTransactioner = (*interestTrackingStoreWithDevices)(nil)
var _ userstore.ProfileLifecycleTransactioner = (*interestTrackingStoreWithRollup)(nil)
var _ userstore.ProfileLifecycleTransactioner = (*interestTrackingStoreWithDevicesAndRollup)(nil)
