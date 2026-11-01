package orgdomains

import (
	"context"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type contextKey int

const (
	requestOrganizationKey contextKey = iota
	explicitOrganizationKey
	brandingWriteKey
)

// WithRequestOrganization records the organization whose registered domain the
// request arrived on. WrapListener sets it.
func WithRequestOrganization(ctx context.Context, org uuid.UUID) context.Context {
	return context.WithValue(ctx, requestOrganizationKey, org)
}

// RequestOrganization is the organization whose registered domain the request
// arrived on; false on any other host.
func RequestOrganization(ctx context.Context) (uuid.UUID, bool) {
	org, ok := ctx.Value(requestOrganizationKey).(uuid.UUID)
	return org, ok && org != uuid.Nil
}

// WithOrganization names the organization presentation is for, for work that
// has no request on that organization's domain, such as an email to one of its
// accounts.
func WithOrganization(ctx context.Context, org uuid.UUID) context.Context {
	return context.WithValue(ctx, explicitOrganizationKey, org)
}

// OrganizationFor is the organization presentation follows: one named with
// WithOrganization, else the authenticated request's organization, else the
// organization of the domain the request arrived on.
func OrganizationFor(ctx context.Context) (uuid.UUID, bool) {
	if org, ok := ctx.Value(explicitOrganizationKey).(uuid.UUID); ok && org != uuid.Nil {
		return org, true
	}
	if tenant, ok := tenancy.FromContext(ctx); ok && tenant.OrganizationID != uuid.Nil {
		return tenant.OrganizationID, true
	}
	return RequestOrganization(ctx)
}

// WithBrandingWrite marks a branding write as org's own. Without it, a write to
// a branding setting changes the server's value, whatever domain it came from.
func WithBrandingWrite(ctx context.Context, org uuid.UUID) context.Context {
	return context.WithValue(ctx, brandingWriteKey, org)
}

// BrandingWriteOrganization is the organization WithBrandingWrite named.
func BrandingWriteOrganization(ctx context.Context) (uuid.UUID, bool) {
	org, ok := ctx.Value(brandingWriteKey).(uuid.UUID)
	return org, ok && org != uuid.Nil
}
