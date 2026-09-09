package models

import "time"

// InviteCode represents a row in the invite_codes table.
type InviteCode struct {
	OrganizationID int64

	ID        int
	Code      string
	Label     string
	MaxUses   int
	UseCount  int
	CreatedBy int
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateInviteCodeInput contains the fields required to create an invite code.
type CreateInviteCodeInput struct {
	OrganizationID int64

	Code      string // if empty, auto-generated
	Label     string
	MaxUses   int
	CreatedBy int
}

// UpdateInviteCodeInput contains optional fields for updating an invite code.
type UpdateInviteCodeInput struct {
	Label   *string
	MaxUses *int
	Enabled *bool
}
