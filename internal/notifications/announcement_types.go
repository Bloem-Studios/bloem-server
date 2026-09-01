package notifications

import (
	"errors"
	"time"
)

// Announcement audiences (docs/specs/client-engagement.md §A.5).
const (
	AudienceAll          = "all"
	AudienceRole         = "role"
	AudienceOrganization = "organization"
	AudienceLibrary      = "library"
	AudienceExplicit     = "explicit"
)

// Announcement errors surfaced by AnnouncementService.
var (
	ErrAnnouncementInvalid      = errors.New("invalid announcement")
	ErrAnnouncementNotFound     = errors.New("announcement not found")
	ErrAnnouncementNoRecipients = errors.New("announcement targets no recipients")
)

// AnnouncementTargeting selects who receives an announcement. Exactly one
// audience applies; the side fields belong to that audience only.
type AnnouncementTargeting struct {
	Audience string `json:"audience"`
	// Role targets every enabled account with this role (admin | user).
	Role string `json:"role,omitempty"`
	// OrganizationID targets active members of one organization.
	OrganizationID string `json:"organization_id,omitempty"`
	// LibraryID targets every profile whose access scope includes the library.
	LibraryID int `json:"library_id,omitempty"`
	// UserIDs / ProfileIDs form the explicit list: every profile of each
	// listed user, plus each listed profile individually.
	UserIDs    []int    `json:"user_ids,omitempty"`
	ProfileIDs []string `json:"profile_ids,omitempty"`
}

// Announcement is one admin compose action and the audit trail for its
// deliveries.
type Announcement struct {
	ID             string                `json:"id"`
	Type           string                `json:"type"`
	Body           AlertBody             `json:"body"`
	Targeting      AnnouncementTargeting `json:"targeting"`
	CreatedBy      *int                  `json:"created_by"`
	RecipientCount int                   `json:"recipient_count"`
	CreatedAt      time.Time             `json:"created_at"`
	WithdrawnAt    *time.Time            `json:"withdrawn_at"`
}

// AnnouncementInput is the admin compose request.
type AnnouncementInput struct {
	Type      string                `json:"type"`
	Body      AlertBody             `json:"body"`
	Targeting AnnouncementTargeting `json:"targeting"`
}

// AnnouncementRecipient is one (user, profile) inbox the fanout writes to.
type AnnouncementRecipient struct {
	UserID    int
	ProfileID string
}
