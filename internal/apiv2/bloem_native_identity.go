package apiv2

import (
	"github.com/danielgtaylor/huma/v2"
)

// The two probes a client runs before it can do anything else: what server is
// this, and which organizations does this account belong to.
//
// Both are Bloem-only. Silo has no organization model at all, so neither will
// ever appear on /api/v2, and a client that wants to key its stored scope by
// server and organization has no upstream contract to read.
//
// Both are served by chi (handlers.ServerIdentityHandler and
// handlers.BloemSystemHandler.HandleOrganizations), so they are declared the
// way the other chi routes are: native {"error","message"} envelopes and only
// the statuses those handlers and their middleware write, not the v2 gate
// class's Problem set.
//
// The shapes restate handlers.serverIdentityResponse and
// handlers.bloemOrganization, which are unexported in a package this one must
// not import. The tests in bloem_native_identity_test.go hold the declarations
// together, the list envelope included, so the restatement cannot drift
// silently.

// BloemServerIdentity is the public identity probe's body.
type BloemServerIdentity struct {
	Status     string `json:"status" enum:"ok" doc:"Always ok. A server that cannot resolve its identity answers 503 instead." example:"ok"`
	ServerID   string `json:"server_id" doc:"Stable identity of this installation. A client keys stored scope by it, so it survives renames and address changes."`
	ServerName string `json:"server_name" doc:"Display name, which may change at any time. Never use it as an identity."`
	// APIVersions and BloemAPI are separate because they answer different
	// questions: which Silo-compatible major versions this build serves, and
	// which Bloem-native surfaces it serves. A client needs both to choose a
	// dialect.
	APIVersions   []int    `json:"api_versions" doc:"Silo-compatible API majors this build serves."`
	BloemAPI      []string `json:"bloem_api" doc:"Native surface versions mounted under /api/bloem/." example:"v1"`
	SetupComplete bool     `json:"setup_complete" doc:"False while the server has no owner account, which is the only state where the setup routes answer."`
}

// BloemServerIdentityOutput is the envelope for the identity probe.
type BloemServerIdentityOutput struct {
	CacheControl string `header:"Cache-Control" doc:"no-store"`
	Body         BloemServerIdentity
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
// belongs to. The body restates handlers.bloemOrganizationListResponse: the
// served object wraps the entries rather than being a bare array. It stays
// anonymous so the client contract's root remains BloemOrganization.
type BloemOrganizationsOutput struct {
	Body struct {
		Organizations []BloemOrganization `json:"organizations" nullable:"false" doc:"Active memberships in active, owned organizations. Empty, never null."`
	}
}

func registerBloemIdentity(reg *Registry) {
	// Unauthenticated by design: a client must be able to ask what a server is
	// before it holds any credential for it. No authentication, rate limit or
	// tenant gate runs in front of it.
	identity := bloemOp("GET", "/server/identity", "getBloemServerIdentity", "system",
		"Which server this is, which surfaces it serves, and whether it has been set up.")
	identity.Security = []map[string][]string{}
	identity.Description = "Public. Mounted even without a database, so a 503 means this server has the endpoint and cannot answer yet; retry rather than falling back to another identity. The 200 response is served with Cache-Control: no-store."
	identity.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &identity, map[int]string{
		503: "unavailable: the installation identity or the setup state could not be read. The body never carries a provisional identifier.",
	})
	registerBloemChiDocument[struct{}, BloemServerIdentityOutput](reg, identity)

	// Authenticated rather than profile-scoped: organization membership hangs
	// off the login account, and a client calls this before it has selected a
	// profile. Only RequireAuth runs in front of it; there is no rate limiter,
	// tenant projection or viewer gate on this route.
	organizations := bloemChiDocumentOp(reg, "GET", "/organizations", "listBloemOrganizations", "tenancy",
		"The organizations this account belongs to.", "bearerAuth", 200)
	organizations.Description = "Requires an account bearer token or API key; no profile or organization needs to be selected. Lists only active memberships in active organizations that have an owner."
	bloemDocumentErrors[BloemNativeError](reg, &organizations, map[int]string{
		401: "unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued.",
		403: "forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first.",
		503: "service_unavailable: the credential could not be checked; retry after Retry-After. tenant_unavailable: memberships or organizations could not be read, or tenant authorization is not configured.",
	})
	organizations.Responses["503"].Headers = map[string]*huma.Header{
		"Retry-After": {Schema: &huma.Schema{Type: "integer"}, Description: "Seconds before retrying; sent with service_unavailable."},
	}
	registerBloemChiDocument[struct{}, BloemOrganizationsOutput](reg, organizations)
}
