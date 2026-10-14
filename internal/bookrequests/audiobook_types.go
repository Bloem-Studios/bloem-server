package bookrequests

import (
	"context"
	"slices"

	"github.com/google/uuid"
)

// AudiobookFulfillment is supplied by the API composition root. Recover never
// calls the plugin: it builds an unknown receipt so a later CheckStatus can
// look for a hand-off whose outcome was lost.
type AudiobookFulfillment interface {
	Fulfill(context.Context, AudiobookDispatchInput) (AudiobookOutcome, error)
	CheckStatus(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error)
	Recover(context.Context, AudiobookDispatchInput) (Receipt, error)
}

// AudiobookIntegration is the frozen identity of an acquisition backend. It
// carries no credentials and no configuration digest: configuration edits do
// not fence an in-flight download. The library, roots and language are frozen
// per dispatch but are deliberately outside the identity fence.
type AudiobookIntegration struct {
	ID             string    `json:"id"`
	Name           string    `json:"-"`
	OrganizationID uuid.UUID `json:"organization_id"`
	OwnerID        uuid.UUID `json:"owner_id"`
	InstallationID int       `json:"installation_id"`
	CapabilityID   string    `json:"capability_id"`
	LibraryID      int       `json:"library_id"`
	DownloadRoots  []string  `json:"download_roots"`
	Language       string    `json:"language"`
}

// sameAudiobookIdentity replaces == (the struct holds a slice) and keeps the
// fence to identity: a rename or rebinding must not fail a running download.
func sameAudiobookIdentity(a, b AudiobookIntegration) bool {
	return a.ID == b.ID && a.OrganizationID == b.OrganizationID && a.OwnerID == b.OwnerID &&
		a.InstallationID == b.InstallationID && a.CapabilityID == b.CapabilityID
}

// AudiobookDispatchInput is everything the adapter may send to the plugin.
// Requester identity is never part of it.
type AudiobookDispatchInput struct {
	Request           Request
	Integration       AudiobookIntegration
	SubmissionID      string
	Attempt           int
	SearchPass        int
	AICalls           int
	ExcludedRefs      []string
	ApprovedCandidate string
}

// Outcome kinds returned by the adapter.
const (
	AudiobookHandedOff      = "handed_off"
	AudiobookNoMatch        = "no_match"
	AudiobookHeld           = "held"
	AudiobookRejected       = "rejected"
	AudiobookUnavailable    = "unavailable"
	AudiobookConfigRejected = "config_rejected"
	AudiobookStatus         = "status"
	AudiobookUncertain      = "uncertain"
)

// AudiobookProviderUnreadable is the status-check answer for a backend that
// could not read its download client. It is never stored: the dispatch keeps
// the request's state and counters, so a client outage never counts toward a
// lost download.
const AudiobookProviderUnreadable = "unreadable"

// AudiobookOutcome is the validated result of one provider call.
type AudiobookOutcome struct {
	Kind           string
	Receipt        Receipt
	ProviderStatus string
	ExternalStatus string
	Decision       *AcquisitionDecision
}

var audiobookExternalStatuses = []string{"queued", "downloading", "stalled", "paused", "verified", "review", "client_error", "deleted", "nzb_fetch_failed", "missing", "absent", "client_changed", "unknown"}

// ValidAudiobookExternalStatus reports whether a plugin external status is in
// the closed vocabulary the host understands.
func ValidAudiobookExternalStatus(status string) bool {
	return slices.Contains(audiobookExternalStatuses, status)
}

// Setter is composition-only, before background tasks begin.
func (s *Service) SetAudiobookFulfillment(f AudiobookFulfillment) { s.audiobookFulfillment = f }

// AudiobookAcquisitionComposed reports whether a host adapter was composed.
func (s *Service) AudiobookAcquisitionComposed() bool {
	return s != nil && s.audiobookFulfillment != nil
}
