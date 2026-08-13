// Package tenancy owns organization membership and protected ownership state.
package tenancy

import (
	"errors"

	"github.com/google/uuid"
)

// OrganizationStatus describes whether an organization may be used.
type OrganizationStatus string

const (
	OrganizationInitializing OrganizationStatus = "initializing"
	OrganizationActive       OrganizationStatus = "active"
	OrganizationSuspended    OrganizationStatus = "suspended"
)

// MembershipStatus describes an account's organization membership state.
type MembershipStatus string

const (
	MembershipInvited   MembershipStatus = "invited"
	MembershipActive    MembershipStatus = "active"
	MembershipSuspended MembershipStatus = "suspended"
)

// Organization is one tenancy boundary.
type Organization struct {
	ID             uuid.UUID          `json:"id"`
	Slug           string             `json:"slug"`
	Name           string             `json:"name"`
	Status         OrganizationStatus `json:"status"`
	OwnerAccountID *int               `json:"owner_account_id,omitempty"`
	PolicyRevision int64              `json:"policy_revision"`
	Default        bool               `json:"is_default"`
}

// Membership associates an account with an organization.
type Membership struct {
	ID               uuid.UUID        `json:"id"`
	OrganizationID   uuid.UUID        `json:"organization_id"`
	AccountID        int              `json:"account_id"`
	Status           MembershipStatus `json:"status"`
	LegacyRole       string           `json:"legacy_role"`
	SecurityRevision int64            `json:"security_revision"`
}

// OwnershipState is the protected ownership state after initial activation.
type OwnershipState struct {
	PlatformOwnerAccountID int
	Organization           Organization
}

var (
	ErrOwnerAlreadyAssigned        = errors.New("owner already assigned")
	ErrOwnershipResolutionRequired = errors.New("ownership resolution required")
	ErrOrganizationNotFound        = errors.New("organization not found")
	ErrMembershipNotFound          = errors.New("membership not found")
	ErrMembershipConflict          = errors.New("membership conflicts with requested state")
	ErrOwnerNotEligible            = errors.New("account is not eligible for ownership")
	ErrTenantNotFoundOrHidden      = errors.New("tenant not found or hidden")
	ErrTenantSuspended             = errors.New("tenant suspended")
	ErrTenantUnavailable           = errors.New("tenant unavailable")
)
