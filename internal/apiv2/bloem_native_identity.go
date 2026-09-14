package apiv2

import (
	"context"
)

// The two probes a client runs before it can do anything else: what server is
// this, and which organizations does this account belong to.
//
// Both are Bloem-only. Silo has no organization model at all, so neither will
// ever appear on /api/v2, and a client that wants to key its stored scope by
// server and organization has no upstream contract to read.
//
// The shapes restate handlers.serverIdentityResponse and
// handlers.bloemOrganization, which are unexported in a package this one must
// not import. TestBloemIdentityDocumentMatchesTheServedShape holds the two
// declarations together so the restatement cannot drift silently.

// BloemServerIdentity is the public identity probe's body.
type BloemServerIdentity struct {
	Status     string `json:"status" doc:"\"ok\" when the server can answer, or a reason it cannot." example:"ok"`
	ServerID   string `json:"server_id" doc:"Stable identity of this installation. A client keys stored scope by it, so it survives renames and address changes."`
	ServerName string `json:"server_name" doc:"Display name, which may change at any time. Never use it as an identity."`
	// APIVersions and BloemAPI are separate because they answer different
	// questions: which Silo-compatible major versions this build serves, and
	// which Bloem-native surfaces it serves. A client needs both to choose a
	// dialect.
	APIVersions   []int    `json:"api_versions" doc:"Silo-compatible API majors this build serves."`
	BloemAPI      []string `json:"bloem_api" doc:"Bloem-native surfaces this build serves." example:"bloem/v1"`
	SetupComplete bool     `json:"setup_complete" doc:"False while the server has no owner account, which is the only state where the setup routes answer."`
}

// BloemServerIdentityOutput is the huma envelope for the identity probe.
type BloemServerIdentityOutput struct {
	Body BloemServerIdentity
}

// BloemOrganization is one organization this account belongs to, with the
// membership that grants it.
type BloemOrganization struct {
	ID             string `json:"id" doc:"Organization identity."`
	Slug           string `json:"slug" doc:"URL-safe short name."`
	Name           string `json:"name" doc:"Display name."`
	Default        bool   `json:"default" doc:"True for the organization a client should select when the viewer has not chosen one."`
	MembershipID   string `json:"membership_id" doc:"The membership granting this account access."`
	MembershipRole string `json:"membership_role" doc:"The role this membership carries."`
	// The two revisions move independently: a policy change and a security
	// change invalidate different caches, so a client that conflates them
	// either over-refetches or serves stale authorization.
	PolicyRevision   int64 `json:"policy_revision" doc:"Increments when this organization's access policy changes."`
	SecurityRevision int64 `json:"security_revision" doc:"Increments when credentials or sessions are invalidated."`
}

// BloemOrganizationsOutput lists the organizations the authenticated account
// belongs to.
type BloemOrganizationsOutput struct {
	Body []BloemOrganization
}

func registerBloemIdentity(reg *Registry) {
	Register(reg, Operation{
		Operation: bloemOp("GET", "/server/identity", "getBloemServerIdentity", "system",
			"Which server this is, which surfaces it serves, and whether it has been set up."),
		// Unauthenticated by design: a client must be able to ask what a server
		// is before it holds any credential for it.
		Class: ClassPublic,
	}, func(context.Context, *struct{}) (*BloemServerIdentityOutput, error) {
		return &BloemServerIdentityOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("GET", "/organizations", "listBloemOrganizations", "tenancy",
			"The organizations this account belongs to."),
		// Authenticated rather than profile-scoped: organization membership
		// hangs off the login account, and a client calls this before it has
		// selected a profile.
		Class: ClassAuthenticated,
	}, func(context.Context, *struct{}) (*BloemOrganizationsOutput, error) {
		return &BloemOrganizationsOutput{}, nil
	})
}
