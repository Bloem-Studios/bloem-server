package userstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ProfileLifecycleWriter is the transaction-scoped surface needed to keep a
// direct profile mutation, canonical setting projections, and its durable
// lifecycle receipt in one caller-owned PostgreSQL transaction.
type ProfileLifecycleWriter interface {
	PreferenceSettingsWriter
	GetProfile(context.Context, string) (*Profile, error)
	DeleteProfile(context.Context, string) error
}

// ErrProfileLifecycleUnsupported reports that the backing store cannot run a
// profile lifecycle mutation inside a caller-owned transaction. Decorators that
// forward the capability unconditionally return it instead of dropping the
// method, so callers see the same "lifecycle safety unavailable" outcome they
// would have got from a failed interface assertion.
var ErrProfileLifecycleUnsupported = errors.New("user store does not support profile lifecycle transactions")

type ProfileLifecycleTransactioner interface {
	WithProfileLifecycleTransaction(context.Context, pgx.Tx, func(ProfileLifecycleWriter) error) error
}
