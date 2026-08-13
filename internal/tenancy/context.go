package tenancy

import (
	"context"

	"github.com/google/uuid"
)

// Context is the resolved tenant identity attached to an authenticated request.
type Context struct {
	OrganizationID     uuid.UUID
	MembershipID       uuid.UUID
	AccountID          int
	OrganizationStatus OrganizationStatus
	MembershipStatus   MembershipStatus
	PolicyRevision     int64
	SecurityRevision   int64
	Legacy             bool
}

type contextKey struct{}

// WithContext stores a resolved tenant context.
func WithContext(ctx context.Context, tenant Context) context.Context {
	return context.WithValue(ctx, contextKey{}, tenant)
}

// FromContext retrieves a resolved tenant context.
func FromContext(ctx context.Context) (Context, bool) {
	tenant, ok := ctx.Value(contextKey{}).(Context)
	return tenant, ok
}
