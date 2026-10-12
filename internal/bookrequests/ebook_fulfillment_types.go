package bookrequests

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Fulfillment is supplied by the host. Recover and CheckStatus must only read
// backend state; an ambiguous Fulfill is never issued a second time.
// ErrProviderNotCalled proves that Fulfill made no provider side effect.
// Transport failures after RPC entry must never use this signal.
var ErrProviderNotCalled = errors.New("book request provider call not started")

type Fulfillment interface {
	Fulfill(context.Context, DispatchInput) (Receipt, error)
	Recover(context.Context, DispatchInput) (Receipt, error)
	CheckStatus(context.Context, DispatchInput, Receipt) (ProviderStatus, error)
}

type DispatchInput struct {
	Request     Request
	Integration EbookIntegration
	// Original submission identity, retained when a cancelled ISBN is refiled.
	IdempotencyKey string
}

// EbookIntegration carries no credentials or provider configuration. The
// revision digest fences dispatch against edits after its descriptor is frozen.
// Public binding reads expose only the connection's name, ID and library.
type EbookIntegration struct {
	ID                           string    `json:"id"`
	Name                         string    `json:"name"`
	LibraryID                    int       `json:"library_id"`
	OrganizationID               uuid.UUID `json:"-"`
	InstallationID               int       `json:"-"`
	CapabilityID                 string    `json:"-"`
	ConfigurationRevision        string    `json:"-"`
	BindingRevision              int64     `json:"-"`
	StorageSourceKey             uuid.UUID `json:"-"`
	StorageConfigurationRevision int64     `json:"-"`
}

type EbookLibrary struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Receipt struct {
	ExternalID string
	Status     string
}

type ProviderStatus struct{ Status string }

// Setter is composition-only, before background tasks begin.
func (s *Service) SetEbookFulfillment(f Fulfillment) { s.ebookFulfillment = f }
