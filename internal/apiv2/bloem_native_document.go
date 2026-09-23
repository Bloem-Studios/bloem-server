package apiv2

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
)

// Bloem's native surface is described the same way Silo's v2 surface is: by
// registering huma operations and generating an OpenAPI document from the
// registrations themselves.
//
// It did not used to be. The native surface was registered on chi, which
// produces no machine-readable contract, and the consequences were not
// theoretical. Asking "does this endpoint exist on /api/v2?" is one query
// against contracts/api/v2/openapi.json; asking the same of /api/bloem/v1 meant
// grepping for path literals, which silently fails because huma builds paths as
// Prefix + "/thing" and chi routes are assembled across nested Route calls. A
// whole duplicate ebook surface was built on that mistake before anyone checked
// the document that already listed it.
//
// The native surface is also what Bloem 1.0 locks. /api/v2 has a committed
// artifact and a breaking-change gate; until this file the surface Bloem itself
// owns had neither.
//
// These files live inside package apiv2 rather than a package of their own so
// they reach humaConfig, humaOp and Registry without exporting them, and
// without editing a single Silo-owned file. Everything Bloem adds here is in a
// bloem_*.go file: no seams, nothing for an upstream merge to conflict with.

// BloemPrefix is the mount of Bloem's native surface. It is the same string as
// handlers.NativeAPIPrefix; the two are separate constants because this package
// must not import the handlers package, and a mismatch is caught by
// TestBloemPrefixMatchesTheMountedRouter.
const BloemPrefix = "/api/bloem/v1"

// bloemAPIMajor is the native surface's major version, carried in the document
// the way APIMajor is for v2.
const bloemAPIMajor = 1

// bloemHumaConfig mirrors humaConfig for the native surface. It deliberately
// reuses the same schema registry naming and security scheme: a client that
// already speaks v2 should not have to learn a second set of conventions to
// speak Bloem's own surface.
func bloemHumaConfig() huma.Config {
	cfg := humaConfig()
	cfg.OpenAPI.Info = &huma.Info{
		Title:       "Bloem Native API",
		Version:     fmt.Sprintf("%d", bloemAPIMajor),
		Description: "Bloem's own surface: features Silo does not have and will not gain, plus the tenancy and entitlement model Bloem adds on top of it.",
	}
	cfg.Components.SecuritySchemes["bloemAccountSession"] = &huma.SecurityScheme{
		Type: "http", Scheme: "bearer",
		Description: "A non-impersonated account access token backed by a login session. Direct-profile tokens, API keys and administrative-context tokens cannot manage profile credentials. Household management authority is checked separately.",
	}
	cfg.Components.SecuritySchemes["bloemOrganizationContext"] = &huma.SecurityScheme{
		Type: "http", Scheme: "bearer",
		Description: "An organization-scoped administrative-context token obtained through /api/bloem/v1/admin/session. The account, originating login session, organization, membership and policy/security revisions are revalidated on every request. An account token or platform-scoped context alone is insufficient.",
	}
	cfg.Components.SecuritySchemes["bloemPlatformContext"] = &huma.SecurityScheme{
		Type: "http", Scheme: "bearer",
		Description: "A platform-scoped administrative-context token obtained through /api/bloem/v1/admin/session. The originating login session and current platform authority are revalidated on every request. Account and organization-scoped tokens are insufficient.",
	}
	return cfg
}

// bloemOp builds a huma.Operation on the native surface. It is humaOp with
// BloemPrefix applied, so a caller writes "/livetv/channels" and cannot
// accidentally register a native operation at a v2 path.
func bloemOp(method, path, id, tag, summary string) huma.Operation {
	return humaOp(method, BloemPrefix+path, id, tag, summary)
}

// registerBloemAll registers every native-surface operation.
//
// Adding an operation here is what puts it in the committed document, so this
// function is the single list of what the native surface promises. A route
// mounted on chi but absent here is invisible to the contract, which is the
// state this whole file exists to end.
func registerBloemAll(reg *Registry) {
	registerBloemCapabilities(reg)
	registerBloemLiveTV(reg)
	registerBloemXtreamDocument(reg)
	registerBloemIdentity(reg)
	registerBloemWatch(reg)
	registerBloemMusic(reg)
	registerBloemNotifications(reg)
	registerBloemPersons(reg)
	registerBloemItemCollections(reg)
	registerBloemProfileCredentialDocument(reg)
	registerBloemOrganizationWorkflowDocument(reg)
	registerBloemEngagementDocument(reg)
	registerBloemSeasonalViewerDocument(reg)
}

// GenerateBloemOpenAPI writes the native surface's OpenAPI document from the Go
// registries alone, exactly as GenerateOpenAPI does for v2: no database, no
// network, no environment, so two runs on any machine produce identical bytes
// and make verify-bloem-openapi can rely on that.
func GenerateBloemOpenAPI() ([]byte, error) {
	api := huma.NewAPI(bloemHumaConfig(), noopAdapter{})
	reg := &Registry{api: api}
	registerBloemAll(reg)
	raw, err := api.OpenAPI().MarshalJSON()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
