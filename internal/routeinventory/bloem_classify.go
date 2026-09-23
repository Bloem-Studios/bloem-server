package routeinventory

// Bloem's route-classification vocabulary. classifyAuth's result is
// independent of rule order (traits are a set; equal-rank rules share a
// class), so Bloem's rules are appended to Silo's tables at init.

const (
	// Bloem traits.
	traitAdminContext   = "admin_context"
	traitLiveTVAccess   = "live_tv_access"
	traitTenantScoped   = "tenant_scoped"
	traitStreamToken    = "stream_token"
	traitAccountSession = "account_session_required"
)

const (
	// Bloem middleware identifiers, spelled as the analyzer prints them.
	markerAdminContext       = "adminMW.Require"
	markerPlatformCredential = "eitherPlatformCredential"
	markerLegacyTenant       = "optionalLegacyTenant"
)

// bodyOrigins.expression also treats bytes.NewReader/NewBuffer as request
// evidence: they re-wrap request bytes a handler already drained. A handler
// that needs the raw body for something else (an idempotency digest, a
// signature check) reads it with io.ReadAll and then decodes the same bytes.
// The reader is still the incoming body, so the decode call downstream must
// still count as request evidence.

var bloemAuthRules = []authRule{
	// Bloem requires a normal verified profile or the freshly resolved profile
	// on a purpose- and path-bound Live TV delivery ticket.
	{marker: "requireBloemLiveTVProfile", class: authProfileScoped, trait: traitProfileReq, rank: 40},
	// Bloem middleware. adminMW.Require validates a platform- or
	// organization-scoped administrative context token and rejects anything
	// else, so it is acting-admin authority on Bloem's own admin surface.
	// eitherPlatformCredential (internal/api/router_bloem.go) dispatches on the
	// bearer prefix: an `sa_` scoped API key goes through RequireAuth, anything
	// else through adminMW.Require, so the route is reachable only with one of
	// the two and carries both traits.
	{marker: markerAdminContext, class: authActingAdmin, trait: traitActingAdmin, rank: 60},
	{marker: markerAdminContext, trait: traitAdminContext},
	// The owned engagement guard further restricts a verified admin context
	// to platform scope; it does not exchange organization authority.
	{marker: "RequireBloemPlatformContext", class: authActingAdmin, trait: traitActingAdmin, rank: 60},
	{marker: "RequireBloemPlatformContext", trait: traitAdminContext},
	{marker: markerPlatformCredential, class: authActingAdmin, trait: traitActingAdmin, rank: 60},
	{marker: markerPlatformCredential, trait: traitAdminContext},
	{marker: markerPlatformCredential, trait: traitAuthenticated},
	// Live TV access is a per-account/profile permission checked against the
	// resolved access scope, like marker edit and metadata curation.
	{marker: "RequireLiveTVAccess", class: authPermissionGated, trait: traitLiveTVAccess, rank: 50},
	{marker: "RequireLiveTVStreamAccess", class: authPermissionGated, trait: traitLiveTVAccess, rank: 50},
}

var bloemTraitOnlyRules = []authRule{
	{marker: "bloemAccountProfileViewer", trait: traitOptionalView},
	// Bloem middleware that qualifies a route without setting its class.
	//
	// optionalLegacyTenant resolves the legacy tenant that scopes the request
	// and is skipped for stream-token-authorized delivery; it authenticates
	// nobody, so it is a scope trait, not a class.
	//
	// StreamTokenAuth only ever adds authorization: a signed token bound to the
	// session in the path lets RequireAuth and RequireViewerAccess pass for the
	// delivery routes. It never rejects, so it cannot lower the route's class.
	//
	// RejectDirectProfileSession subtracts rather than adds: a direct-profile
	// session is refused on account- and household-scoped surfaces. The route
	// still needs whatever its class demands.
	//
	// The compatibility listener builds its own rate limiter (`s.rateLimit`)
	// instead of the api listener's RateLimitMW.
	{marker: markerLegacyTenant, trait: traitTenantScoped},
	{marker: ".tenant.ResolveNative", trait: traitTenantScoped},
	{marker: "StreamTokenAuth", trait: traitStreamToken},
	{marker: "bloemLiveTVStreamTokens", trait: traitStreamToken},
	{marker: "RejectDirectProfileSession", trait: traitAccountSession},
	{marker: "rateLimit.Handler", trait: traitRateLimited},
	// router_bloem.go supplies AuthEndpointHandler("profile_credentials").
	{marker: "surfaces.ProfileCredentialLimit", trait: traitRateLimited},
}

var bloemInfrastructureMiddleware = []string{
	// Bloem infrastructure: X-Bloem-* header folding onto the canonical
	// X-Silo-* names, the lifecycle-idempotency phase preflight, the
	// compatibility listener's prefix strip and its trace-id propagation.
	// None of them authenticates or authorizes.
	"apimw.NormalizeClientHeaders", "preflight.Handler", "compatapi.RelativePaths", "traceMiddleware",
}

func init() {
	authRules = append(authRules, bloemAuthRules...)
	traitOnlyRules = append(traitOnlyRules, bloemTraitOnlyRules...)
	infrastructureMiddleware = append(infrastructureMiddleware, bloemInfrastructureMiddleware...)
}
