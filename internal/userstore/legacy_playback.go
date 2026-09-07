package userstore

import "context"

type legacyPlaybackWriteKey struct{}

// WithLegacyPlaybackWrite identifies persistence originating from an unbound
// playback session, including delayed stop and expiry callbacks. It grants no
// authority. Providers use it to serialize these writes with first admission;
// manual watch-state edits and imports deliberately do not carry this marker.
func WithLegacyPlaybackWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, legacyPlaybackWriteKey{}, true)
}

func IsLegacyPlaybackWrite(ctx context.Context) bool {
	marked, _ := ctx.Value(legacyPlaybackWriteKey{}).(bool)
	return marked
}

// LegacyPlaybackAdmissionProvider serializes an entire legacy launch against
// first admission. The returned context must be passed to nested persistence;
// release must be idempotent and run on every return after launch/rollback.
type LegacyPlaybackAdmissionProvider interface {
	AcquireLegacyPlaybackAdmission(context.Context, int) (context.Context, func(), error)
}

// AcquireLegacyPlaybackAdmission preserves other providers' existing behavior.
// Only PostgreSQL first admission is implemented; this is not SQLite enrollment.
func AcquireLegacyPlaybackAdmission(ctx context.Context, provider UserStoreProvider, accountID int) (context.Context, func(), error) {
	if guarded, ok := provider.(LegacyPlaybackAdmissionProvider); ok {
		return guarded.AcquireLegacyPlaybackAdmission(ctx, accountID)
	}
	return ctx, func() {}, nil
}
