package access

import "github.com/google/uuid"

// TenantGroup embeds upstream configuration and retains tenant policy authority.
type TenantGroup struct {
	Group
	OrganizationID          uuid.UUID
	PlaybackAllowed         bool
	MaxProfiles             int
	ManagedTemplateKey      *string
	ManagedTemplateRevision *int64
	ManagedCohortID         uuid.UUID
}

func (g TenantGroup) Policy() GroupPolicy {
	policy := g.Group.Policy()
	policy.PlaybackAllowed = g.PlaybackAllowed
	policy.MaxProfiles = g.MaxProfiles
	return policy
}

type TenantCreateGroupInput struct {
	CreateGroupInput
	PlaybackAllowed *bool
	MaxProfiles     int
}
type TenantUpdateGroupInput struct {
	UpdateGroupInput
	PlaybackAllowed *bool
	MaxProfiles     *int
}
