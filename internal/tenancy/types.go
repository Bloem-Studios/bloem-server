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
	ID             uuid.UUID
	Slug           string
	Name           string
	Status         OrganizationStatus
	OwnerAccountID *int
	PolicyRevision int64
	Default        bool
}

// Membership associates an account with an organization.
type Membership struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	AccountID        int
	Status           MembershipStatus
	LegacyRole       string
	SecurityRevision int64
}

// OwnershipState is the protected ownership state after initial activation.
type OwnershipState struct {
	PlatformOwnerAccountID int
	Organization           Organization
}

var (
	ErrOwnerAlreadyAssigned        = errors.New("owner already assigned")
	ErrOwnershipResolutionRequired = errors.New("ownership resolution required")
	ErrMembershipNotFound          = errors.New("membership not found")
)
