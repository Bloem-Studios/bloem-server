package resourcetenancy

import (
	"errors"

	"github.com/google/uuid"
)

type OwnerKind string

const (
	OwnerPlatform     OwnerKind = "platform"
	OwnerOrganization OwnerKind = "organization"
)

type RootKind string

const (
	RootMediaFolder        RootKind = "media_folder"
	RootPluginInstallation RootKind = "plugin_installation"
)

type EntitlementStatus string

const (
	EntitlementActive    EntitlementStatus = "active"
	EntitlementSuspended EntitlementStatus = "suspended"
	EntitlementRevoked   EntitlementStatus = "revoked"
)

type RootRef struct {
	Kind RootKind
	ID   int64
}

type Owner struct {
	ID             uuid.UUID
	Kind           OwnerKind
	OrganizationID *uuid.UUID
	Revision       int64
}

type Entitlement struct {
	ID                   uuid.UUID
	OrganizationID       uuid.UUID
	Root                 RootRef
	RootOwnerID          uuid.UUID
	Status               EntitlementStatus
	SourceBundleID       *uuid.UUID
	SourceBundleRevision *int64
	SecurityRevision     int64
}

type Grant struct {
	Root        RootRef
	Owner       Owner
	Entitlement *Entitlement
}

var (
	ErrResourceHidden           = errors.New("resource not found")
	ErrResourceUnavailable      = errors.New("resource scope unavailable")
	ErrInvalidRoot              = errors.New("invalid resource root")
	ErrOrganizationUnavailable  = errors.New("organization unavailable")
	ErrDefaultBundleUnavailable = errors.New("default entitlement bundle unavailable")
	ErrInvalidActor             = errors.New("invalid entitlement actor")
)
