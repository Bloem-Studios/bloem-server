package apiv2

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

const (
	bloemPlatformContextSecurity     = "bloemPlatformContext"
	bloemOrganizationContextSecurity = "bloemOrganizationContext"
)

// This is a document of the existing chi contract, never a runtime registrar.
// Responses reuse domain types where the handlers serialize them directly.
// Request types keep handler defaults and scope-specific field rejection.
type BloemNativeStorageSourceMedia struct {
	SourceKind string   `json:"source_kind" enum:"filesystem,s3,bookwarehouse"`
	MediaKinds []string `json:"media_kinds" nullable:"false"`
}
type BloemNativeStorageCapabilities struct {
	LibraryLocationBrowsing      bool                            `json:"library_location_browsing" doc:"The retained host browser and shared pagination store are composed. Individual source authority and media declarations are checked separately."`
	LibraryLocationAtomicEditing bool                            `json:"library_location_atomic_editing" doc:"Scoped library-locations operations are composed with the ordinary library creator. Replacement requires revision and actor-bound path edit token."`
	LibraryLocationMixing        bool                            `json:"library_location_mixing" doc:"One library may combine filesystem paths with plugin sources or use several plugin sources. While false, a library keeps filesystem paths or one plugin source over its lifetime, and other shapes return location_mixing_unavailable."`
	SupportedSourceMedia         []BloemNativeStorageSourceMedia `json:"supported_source_media" nullable:"false" doc:"Host-supported source/media combinations; this does not prove that a provider instance supports or is available for a kind."`
	NativeAudioLibraries         bool                            `json:"native_audio_libraries" doc:"Requires the complete native audio scanner, catalog and playback composition."`
	NativeAudioPlayback          bool                            `json:"native_audio_playback" doc:"Always equal to native_audio_libraries: true only when the native audio scanner, catalog and playback are all composed."`

	Schema                      int  `json:"schema" enum:"2"`
	SourceManagement            bool `json:"source_management"`
	ApprovedArtifactInstall     bool `json:"approved_artifact_install"`
	ConfigurationReplaceUnbound bool `json:"configuration_replace_unbound"`
	Disable                     bool `json:"disable"`
	Uninstall                   bool `json:"uninstall"`
	Upgrade                     bool `json:"upgrade"`
	CredentialRotationInUse     bool `json:"credential_rotation_in_use" doc:"Credential rotation is composed and works on a source that backs a library. It overlays only access key, secret key and session token and needs an enabled source and its current revision."`
	S3InitialRootDiscovery      bool `json:"s3_initial_root_discovery"`
	S3CatalogDiscovery          bool `json:"s3_catalog_discovery" doc:"Administrators can add an S3 source from a catalog release whose digest the host has pre-approved, discovering its root without a manual upload. Platform administrators create a platform-owned source and organization administrators one owned by their organization."`
	S3BucketBrowse              bool `json:"s3_bucket_browse" doc:"The stateless S3 bucket and folder browse operations are composed. Organization administrators are limited to well-known S3 endpoint ports."`
	S3CheckedETagReads          bool `json:"s3_checked_etag_reads" doc:"Explicit checked-etag admission is supported for approved S3 source configuration. This does not enable a source or prove immutable bytes."`
	NativeMovieLibraries        bool `json:"native_movie_libraries" doc:"Movie attachment and scanning are composed with native video playback; individual sources still require admission under their retained read policy."`
	NativeVideoPlayback         bool `json:"native_video_playback" doc:"Native API video playback is composed; this does not certify a backend or client runtime."`
	NativeVideoRemoteExecution  bool `json:"native_video_remote_execution" doc:"Shared worker grants and header-auth serving recipes are composed for remote HLS; the selected worker still needs its own approved runtime and retained provider identity."`
	NativeVideoDownloads        bool `json:"native_video_downloads" enum:"false"`
	NativeVideoJellyfin         bool `json:"native_video_jellyfin" enum:"false"`
	RetainedNamespaceReinstall  bool `json:"retained_namespace_reinstall" enum:"false"`
	Enable                      bool `json:"enable" enum:"false"`
	BackendVerified             bool `json:"backend_verified" enum:"false"`
}

type BloemNativeStorageArtifacts struct {
	Artifacts []handlers.NativeStorageArtifactResponse `json:"artifacts" nullable:"false"`
}

type BloemNativeStorageSource struct {
	Source nativestorage.SourceView `json:"source"`
}

type BloemNativeStorageInstallBody struct {
	ArtifactKey      string                    `json:"artifact_key" minLength:"1" doc:"Host-approved artifact key, at most 1024 UTF-8 bytes; the upload cannot approve its own manifest or digest."`
	ProviderSourceID string                    `json:"provider_source_id" minLength:"1" doc:"Opaque provider source identity, at most 1024 UTF-8 bytes."`
	RootEntryID      string                    `json:"root_entry_id" minLength:"1" doc:"Opaque provider root identity, at most 1024 UTF-8 bytes; not a mounted path."`
	Enabled          bool                      `json:"enabled"`
	SourceKey        *uuid.UUID                `json:"source_key,omitempty" nullable:"true" doc:"Retained source to reinstall. Requires expected_revision, verified lineage and an empty namespace; populated reattachment is unsupported."`
	ExpectedRevision *int64                    `json:"expected_revision,omitempty" nullable:"true" minimum:"1" doc:"Required when source_key is nonnull; otherwise omit or null. Current source configuration revision."`
	Config           map[string]map[string]any `json:"config" writeOnly:"true" doc:"Complete provider configuration. Empty object is valid; each value must be an object. Encrypted at rest, never returned."`
}

type BloemNativeStoragePlatformInstallBody struct {
	BloemNativeStorageInstallBody
	OrganizationID *uuid.UUID `json:"organization_id,omitempty" nullable:"true" doc:"Optional active organization ownership. Omitted or null uses platform ownership. Organization-scoped routes reject this field."`
}

type BloemNativeStorageDiscoverInstallBody struct {
	ArtifactKey      string                    `json:"artifact_key" minLength:"1" doc:"Host-approved bloem.storage.s3 artifact key, at most 1024 UTF-8 bytes. The upload cannot approve its own artifact."`
	ProviderSourceID string                    `json:"provider_source_id" minLength:"1" doc:"Exact opaque provider source to select from a fresh Describe, at most 1024 UTF-8 bytes. Its root is discovered, never supplied by the caller."`
	Enabled          bool                      `json:"enabled" doc:"Committed enabled state. Even false requires a configured temporary discovery process."`
	Config           map[string]map[string]any `json:"config" writeOnly:"true" doc:"Complete configuration for discovery. Caller state_path is forbidden. Encrypted at rest, never returned."`
}

type BloemNativeStorageCredentialsBody struct {
	ExpectedRevision int64  `json:"expected_revision" minimum:"1" doc:"Current source configuration revision. The source must be enabled."`
	AccessKey        string `json:"access_key" minLength:"1" writeOnly:"true" doc:"Replacement S3 access key id. No control characters. Encrypted at rest, never returned."`
	SecretKey        string `json:"secret_key" minLength:"1" writeOnly:"true" doc:"Replacement S3 secret access key. No control characters. Encrypted at rest, never returned."`
	SessionToken     string `json:"session_token,omitempty" writeOnly:"true" doc:"Optional replacement session token; omitted or empty clears any stored token. Encrypted at rest, never returned."`
}

type BloemNativeStorageS3BucketsBody struct {
	Endpoint     string `json:"endpoint" minLength:"1" maxLength:"2048" doc:"S3-compatible https origin, for example https://s3.example.com:9000. No userinfo, path, query or fragment. Every resolved address must be public; organization administrators may only use ports 443, 7480, 8443, 9000 and 9443. Trimmed."`
	Region       string `json:"region" minLength:"1" doc:"Signing region: lowercase letters, digits and hyphens, at most 64. Trimmed."`
	AccessKey    string `json:"access_key" minLength:"1" writeOnly:"true" doc:"S3 access key id, at most 1024 bytes. Used for this request only; never stored, logged or returned. Trimmed."`
	SecretKey    string `json:"secret_key" minLength:"1" writeOnly:"true" doc:"S3 secret access key, at most 4096 bytes. Used for this request only; never stored, logged or returned. Trimmed."`
	SessionToken string `json:"session_token,omitempty" writeOnly:"true" doc:"Optional session token, at most 8192 bytes. Used for this request only; never stored, logged or returned. Trimmed."`
}

type BloemNativeStorageS3FoldersBody struct {
	BloemNativeStorageS3BucketsBody
	Bucket string `json:"bucket" minLength:"1" doc:"Bucket to list, as returned by the bucket listing. Trimmed."`
	Prefix string `json:"prefix,omitempty" doc:"Folder to list: empty for the bucket root, else a prefix ending in /, as returned in folders[].prefix. UTF-8, at most 1024 bytes, no backslash, control characters or . and .. segments. Not trimmed."`
	Cursor string `json:"cursor,omitempty" maxLength:"4096" doc:"Opaque next_cursor of the previous page for the same bucket and prefix, at most 4096 bytes; omit for the first page."`
}

type BloemNativeStorageS3Buckets struct {
	Buckets []nativestorage.S3Bucket `json:"buckets" nullable:"false" doc:"Bucket names usable as an S3 source, sorted ascending, at most 1000."`
}

type BloemNativeStorageS3Folders struct {
	Prefix     string                   `json:"prefix" doc:"The listed prefix."`
	Folders    []nativestorage.S3Folder `json:"folders" nullable:"false" doc:"Immediate child folders only, at most 200; files are never returned. A page may be empty and still continue."`
	NextCursor string                   `json:"next_cursor" doc:"Opaque continuation token for the next page; empty when the listing is complete."`
}

type BloemNativeStoragePlatformDiscoverInstallBody struct {
	BloemNativeStorageDiscoverInstallBody
	OrganizationID *uuid.UUID `json:"organization_id,omitempty" nullable:"true" doc:"Optional active organization ownership. Omitted or null uses platform ownership. Organization-scoped routes reject this field, including null."`
}

type BloemNativeStorageUpgradeBody struct {
	ArtifactKey      string    `json:"artifact_key" minLength:"1" doc:"Host-approved artifact of the same plugin, at most 1024 UTF-8 bytes; the upload cannot approve its own manifest or digest."`
	SourceKey        uuid.UUID `json:"source_key" doc:"The source this installation serves."`
	ExpectedRevision int64     `json:"expected_revision" minimum:"1" doc:"Current source configuration revision; the upgrade keeps it."`
}

type BloemNativeStorageConfigurationBody struct {
	ExpectedRevision int64                     `json:"expected_revision" minimum:"1" doc:"Current source configuration revision. Replacement requires an empty namespace without bindings, entries or references."`
	Config           map[string]map[string]any `json:"config" writeOnly:"true" doc:"Complete replacement configuration; each value must be an object."`
}

type BloemNativeStorageConfiguration struct {
	SourceKey             uuid.UUID `json:"source_key"`
	ConfigurationRevision int64     `json:"configuration_revision"`
}

type BloemNativeStorageRemoveBody struct {
	SourceKey        uuid.UUID `json:"source_key"`
	ExpectedRevision int64     `json:"expected_revision" minimum:"1" doc:"Current source configuration revision, not the library revision."`
}

type BloemNativeStorageRemoval struct {
	InstallationID int    `json:"installation_id"`
	State          string `json:"state" enum:"disabled_detached,uninstalled_detached"`
	Retained       bool   `json:"retained" enum:"true"`
}

// Recovery fields are conditional and omitted, never null. Their presence is
// restricted to identifiers established after current resource authorization.
type BloemNativeStorageError struct {
	Error                  string     `json:"error"`
	Message                string     `json:"message"`
	CurrentRevision        *int64     `json:"current_revision,omitempty" nullable:"false"`
	CurrentLibraryRevision *int64     `json:"current_library_revision,omitempty" nullable:"false"`
	LibraryID              *int       `json:"library_id,omitempty" nullable:"false"`
	CurrentSourceRevision  *int64     `json:"current_source_revision,omitempty" nullable:"false"`
	OperationID            *uuid.UUID `json:"operation_id,omitempty" nullable:"false"`
	Operation              *string    `json:"operation,omitempty" nullable:"false" enum:"install,upgrade,configuration,disable,uninstall,create_library,replace_library_locations"`
	SourceKey              *uuid.UUID `json:"source_key,omitempty" nullable:"false"`
}

type bloemNativeStorageOutput[T any] struct{ Body T }

func bloemNativeStorageJSONBody[T any](reg *Registry) *huma.RequestBody {
	return &huma.RequestBody{
		Required:    true,
		Description: "Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.",
		Content: map[string]*huma.MediaType{
			"application/json": {Schema: reg.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[T](), true, "")},
		},
	}
}

func bloemNativeStorageS3JSONBody[T any](reg *Registry) *huma.RequestBody {
	body := bloemNativeStorageJSONBody[T](reg)
	body.Description = "Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Credentials are write-only and never stored."
	return body
}

func bloemNativeStorageMultipart[T any](reg *Registry) *huma.RequestBody {
	return &huma.RequestBody{
		Required:    true,
		Description: "Exactly two parts, request JSON (at most 1 MiB) and nonempty binary (at most 256 MiB); total at most 258 MiB. Duplicate/extra parts and Content-Transfer-Encoding are rejected. Request keys are exact, unique and bounded; no trailing JSON.",
		Content: map[string]*huma.MediaType{
			"multipart/form-data": {
				Schema: &huma.Schema{Type: "object", AdditionalProperties: false, Required: []string{"request", "binary"}, Properties: map[string]*huma.Schema{
					"request": reg.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[T](), true, ""),
					"binary":  {Type: "string", Format: "binary", Description: "Approved executable artifact bytes; at most 256 MiB, nonempty."},
				}},
				Encoding: map[string]*huma.Encoding{"request": {ContentType: "application/json"}, "binary": {ContentType: directMediaBinary}},
			},
		},
	}
}

// bloemNativeStorageForbidden is the 403 of every scoped native-storage and
// library-location operation: the administrative middleware and the
// handler's own scope check.
const bloemNativeStorageForbidden = "insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended."

func bloemNativeStorageDocument[T any](reg *Registry, scope, method, path, id, summary string, status int, body *huma.RequestBody, list bool) {
	security, label := bloemPlatformContextSecurity, "Platform"
	if scope == "organization" {
		security, label = bloemOrganizationContextSecurity, "Organization"
	}
	op := bloemChiDocumentOp(reg, method, "/admin/"+scope+"/native-storage"+path, id+"Bloem"+label+"NativeStorage", "native-storage", summary, security, status)
	op.Description = "Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them."
	if note := bloemNativeStorageNotes[path]; note != "" {
		op.Description += " " + note
	}
	op.RequestBody = body
	one, hundred := float64(1), float64(100)
	for _, name := range []string{"source_key", "installation_id"} {
		if !strings.Contains(path, "{"+name+"}") {
			continue
		}
		schema := &huma.Schema{Type: "string", Format: "uuid"}
		if strings.HasSuffix(name, "_id") {
			schema = &huma.Schema{Type: "integer", Minimum: &one}
		}
		op.Parameters = append(op.Parameters, &huma.Param{Name: name, In: "path", Required: true, Schema: schema})
	}
	if list {
		after := &huma.Schema{Type: "string", Format: "uuid"}
		op.Parameters = append(op.Parameters,
			&huma.Param{Name: "after", In: "query", Schema: after, Description: "Exclusive keyset cursor returned as next_after; omit on the first page."},
			&huma.Param{Name: "limit", In: "query", Schema: &huma.Schema{Type: "integer", Minimum: &one, Maximum: &hundred, Default: 50}})
	}
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{403: bloemNativeStorageForbidden})
	bloemDocumentErrors[BloemNativeStorageError](reg, &op, bloemNativeStorageErrors(scope, path))
	op.Responses[strconv.Itoa(status)] = &huma.Response{Description: summary}
	for _, response := range op.Responses {
		response.Headers = map[string]*huma.Header{"Cache-Control": {Description: "Native management handler responses are not cacheable.", Schema: &huma.Schema{Type: "string", Enum: []any{"no-store"}}}}
	}
	if strings.HasPrefix(path, "/s3/") || path == "/catalog/installations/discover" {
		op.Responses["503"].Headers["Retry-After"] = &huma.Header{Description: "Seconds to wait; sent when a rate limit or concurrency cap refused the request.", Schema: &huma.Schema{Type: "integer"}}
	}
	registerBloemChiDocument[struct{}, bloemNativeStorageOutput[T]](reg, op)
}

// bloemNativeStorageNotes adds operation-specific rules to the shared description.
var bloemNativeStorageNotes = map[string]string{
	"/installations/discover":                          "Only a host-approved bloem.storage.s3 artifact qualifies. The provider runs once in a temporary process to report the selected source's root; the caller never supplies a root, source key or state_path.",
	"/catalog/installations":                           "S3 sources are added only through catalog discovery: an S3 catalog key is artifact_rejected for a platform administrator and not_found for an organization administrator. A key with no catalog release, or a failed download or checksum, is native_storage_unavailable.",
	"/catalog/installations/{installation_id}/upgrade": "An organization administrator receives not_found for the S3 plugin, also for an S3 source the organization owns; a platform administrator, or a manual upload of an approved artifact, upgrades it. A key with no catalog release, or a failed download or checksum, is native_storage_unavailable.",
	"/sources/{source_key}/configuration":              "The source and its installation must be enabled (otherwise not_found) and attached (otherwise native_storage_unavailable). The configuration must satisfy the installed artifact's declared schema (otherwise invalid_request).",
	"/sources/{source_key}/credentials":                "Only an enabled S3 source qualifies: another plugin's source or a disabled one is not_found, and a detached one native_storage_unavailable.",
}

// bloemNativeStorageErrors lists what each native-storage handler, its domain
// calls and the administrative middleware can write besides 401 and 403.
func bloemNativeStorageErrors(scope, path string) map[int]string {
	const (
		authority      = "tenant_unavailable: administrative authority could not be checked."
		unavailable    = "native_storage_unavailable: native storage, the source, its provider or the database is unavailable."
		unknown        = " mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback."
		catalog        = " native_storage_unavailable also covers an unavailable catalog, a key with no catalog release and a failed download or checksum."
		read           = "invalid_request: query parameters or a request body were sent."
		strictJSON     = "invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values."
		strictForm     = "invalid_request: query parameters were sent, or the body is not exactly one request part and one nonempty binary part, or the request JSON has unknown, duplicate, missing or invalid fields."
		hidden         = "not_found: invalid path identity, absent resource or hidden authority."
		revision       = "revision_conflict returns current_revision, the source's current configuration revision."
		jsonLarge      = "request_too_large: the JSON body exceeds 1 MiB."
		configLarge    = "request_too_large: the JSON body or the serialized configuration exceeds 1 MiB."
		formLarge      = "request_too_large: the request part exceeds 1 MiB, the binary 256 MiB, the whole body 258 MiB, or the serialized configuration 1 MiB."
		rejectedUpload = "artifact_rejected: the artifact is not host-approved, the uploaded bytes do not match it, or the configuration does not satisfy its declared schema."
		rejectedSchema = " or the configuration does not satisfy its declared schema."
		endpointGuard  = "s3_endpoint_rejected: config connection.endpoint (S3) or connection.base_url (Bookwarehouse) is not an https URL on a well-known S3 port whose host resolves only to public addresses, or allow_loopback_http or allow_insecure_http is set; case variants of these keys are invalid_request."
	)
	// Platform administrators may name an owning organization; organization
	// administrators always act for their own organization.
	owner, retained := "not_found: the owner named by organization_id is absent or hidden.", "not_found: the retained source_key, or the owner named by organization_id, is absent or hidden."
	catalogS3, catalogS3Hidden := "the key names the S3 plugin, ", ""
	if scope == "organization" {
		owner, retained = "not_found: the actor's storage owner is no longer visible.", "not_found: the retained source_key is absent or hidden, or the actor's storage owner is no longer visible."
		catalogS3, catalogS3Hidden = "", " An S3 catalog key is also not_found."
	}
	r := map[int]string{}
	switch path {
	case "/capabilities":
		r[400] = read
	case "/artifacts":
		r[400], r[503] = read, "native_storage_unavailable: the catalog could not be refreshed and no artifact is approved."
	case "/sources":
		r[400] = "invalid_request: unknown, repeated or empty query parameters, a limit outside 1-100, an invalid after, or a request body."
		r[404] = "not_found: the actor's storage owner, or a source on the page, is no longer visible."
		r[503] = unavailable
	case "/sources/{source_key}":
		r[400], r[404], r[503] = read, hidden, unavailable
	case "/installations":
		r[400] = strictForm + " organization_id is rejected in organization scope, and source_key and expected_revision go together."
		r[404] = retained
		r[409] = revision + " source_attached: the source is already attached, enabled or present. retained_namespace_unverified: the retained source's identity differs from the request or its namespace is populated."
		r[413], r[422], r[503] = formLarge, rejectedUpload, unavailable+unknown
	case "/installations/discover":
		r[400] = strictForm + " organization_id is rejected in organization scope, and config must hold only the connection namespace, without state_path."
		r[404] = owner
		r[409] = "source_attached: the generated source identity already exists."
		r[413] = formLarge
		r[422] = "artifact_rejected: the artifact is not a host-approved bloem.storage.s3 artifact, the uploaded bytes do not match it," + rejectedSchema
		r[503] = unavailable + " This includes discovery not being composed and the provider failing to start or describe the source within 30 seconds." + unknown
	case "/catalog/installations":
		r[400] = strictJSON + " organization_id is rejected in organization scope, and source_key and expected_revision go together."
		r[404] = retained + catalogS3Hidden
		r[409] = revision + " source_attached: the source is already attached, enabled or present. retained_namespace_unverified: the retained source's identity differs from the request or its namespace is populated."
		r[413] = configLarge
		r[422] = "artifact_rejected: " + catalogS3 + "the release is not an approved artifact," + rejectedSchema
		r[503] = unavailable + catalog + unknown
	case "/catalog/installations/discover":
		r[400] = strictJSON + " Any field besides artifact_key, provider_source_id, enabled and config, including organization_id, is rejected, as is a config with state_path or with namespaces other than connection."
		r[404] = "not_found: the actor's storage owner is no longer visible."
		r[409] = "source_attached: the generated source identity already exists."
		r[413] = configLarge
		r[422] = "artifact_rejected: the key is not an admitted S3 catalog release," + rejectedSchema
		r[503] = unavailable + " This includes the per-administrator rate limit (10 per minute, burst 3; Retry-After is set), discovery not being composed, and the provider failing to start or describe the source within 30 seconds." + catalog + unknown
	case "/catalog/installations/{installation_id}/upgrade":
		r[400] = strictJSON
		r[404] = hidden + " An organization administrator also receives it for the S3 plugin."
		r[409], r[413] = revision, jsonLarge
		r[422] = "artifact_rejected: the release is not an approved artifact or is for a different plugin."
		r[503] = unavailable + catalog + unknown
	case "/installations/{installation_id}/upgrade":
		r[400] = strictForm
		r[404], r[409] = hidden, revision
		r[413] = "request_too_large: the request part exceeds 1 MiB, the binary 256 MiB or the whole body 258 MiB."
		r[422] = "artifact_rejected: the artifact is not host-approved, the uploaded bytes do not match it, or it is for a different plugin."
		r[503] = unavailable + unknown
	case "/sources/{source_key}/configuration":
		r[400] = strictJSON + " The configuration must also satisfy the installed artifact's declared schema."
		r[404] = hidden
		r[409] = revision + " configuration_namespace_unverified: a source of this installation has a library location, entries or file references."
		r[413], r[503] = configLarge, unavailable+unknown
	case "/sources/{source_key}/credentials":
		r[400] = strictJSON + " Credentials must be nonempty (session_token may be empty) and free of control characters."
		r[404], r[409], r[413], r[503] = hidden, revision, jsonLarge, unavailable+unknown
	case "/installations/{installation_id}/disable", "/installations/{installation_id}":
		r[400] = strictJSON
		r[404], r[409], r[413], r[503] = hidden, revision, jsonLarge, unavailable+unknown
	case "/s3/buckets", "/s3/folders":
		r[400] = strictJSON + " Region, credential, bucket, prefix and cursor bounds apply as documented on the fields."
		r[413] = jsonLarge
		r[422] = "s3_endpoint_rejected: the endpoint is not an https origin, has userinfo, a path, query or fragment, an invalid or (organization scope) non-S3 port, a host resolving to any loopback, private, link-local, unique-local, CGNAT, multicast, unspecified or metadata address, or a host that does not resolve. s3_access_denied: the backend refused the credentials."
		r[502] = "s3_backend_error: the backend failed, timed out, redirected or returned a malformed or oversized response. Messages are fixed and never quote the backend."
		r[503] = "native_storage_unavailable: browsing is not composed, or the per-administrator rate limit (30 per minute), the per-administrator cap of two concurrent requests or the server-wide cap of four (one reserved for platform administrators) refused the request; Retry-After is set for those refusals."
	}
	// Every route also passes the administrative middleware first.
	r[503] = strings.TrimSpace(r[503] + " " + authority)
	switch path {
	case "/installations", "/installations/discover", "/catalog/installations", "/catalog/installations/discover", "/sources/{source_key}/configuration":
		if scope == "organization" {
			if r[422] == "" {
				r[422] = endpointGuard
			} else {
				r[422] += " " + endpointGuard
			}
		}
	}
	return r
}

func registerBloemNativeStorageDocument(reg *Registry) {
	for _, scope := range []string{"platform", "organization"} {
		bloemNativeStorageDocument[BloemNativeStorageCapabilities](reg, scope, "GET", "/capabilities", "getCapabilities", "Read composition-dependent flags; backend verification remains false.", 200, nil, false)
		bloemNativeStorageDocument[BloemNativeStorageArtifacts](reg, scope, "GET", "/artifacts", "listArtifacts", "List automatically verified catalog releases and any optional manual artifacts.", 200, nil, false)
		bloemNativeStorageDocument[nativestorage.SourcePage](reg, scope, "GET", "/sources", "listSources", "List visible sources and their current configuration revisions.", 200, nil, true)
		bloemNativeStorageDocument[BloemNativeStorageSource](reg, scope, "GET", "/sources/{source_key}", "getSource", "Read a visible source without configuration secrets.", 200, nil, false)
		install := bloemNativeStorageMultipart[BloemNativeStorageInstallBody](reg)
		if scope == "platform" {
			install = bloemNativeStorageMultipart[BloemNativeStoragePlatformInstallBody](reg)
		}
		bloemNativeStorageDocument[BloemNativeStorageSource](reg, scope, "POST", "/installations", "install", "Install an approved artifact and source without creating a library or scan.", 201, install, false)
		discoverInstall := bloemNativeStorageMultipart[BloemNativeStorageDiscoverInstallBody](reg)
		if scope == "platform" {
			discoverInstall = bloemNativeStorageMultipart[BloemNativeStoragePlatformDiscoverInstallBody](reg)
		}
		bloemNativeStorageDocument[BloemNativeStorageSource](reg, scope, "POST", "/installations/discover", "discoverInstall", "Discover the root of a new approved S3 installation and publish it atomically without a library or scan.", 201, discoverInstall, false)
		catalogInstall := bloemNativeStorageJSONBody[BloemNativeStorageInstallBody](reg)
		if scope == "platform" {
			catalogInstall = bloemNativeStorageJSONBody[BloemNativeStoragePlatformInstallBody](reg)
		}
		bloemNativeStorageDocument[BloemNativeStorageSource](reg, scope, "POST", "/catalog/installations", "catalogInstall", "Download and checksum-verify the trusted catalog plugin, then install without manual approval or binary upload.", 201, catalogInstall, false)
		bloemNativeStorageDocument[BloemNativeStorageSource](reg, scope, "POST", "/catalog/installations/discover", "catalogDiscoverInstall", "Download the trusted catalog plugin (an S3 release must match a pre-approved digest), discover the root of the selected source and publish it atomically without a library or scan. The source is owned by the actor's scope: the platform, or the organization administrator's own organization; organization_id is rejected. Rate limited to 10 per minute per administrator (503 native_storage_unavailable with Retry-After); a key that is not an admitted S3 catalog release is artifact_rejected before any download.", 201, bloemNativeStorageJSONBody[BloemNativeStorageDiscoverInstallBody](reg), false)
		bloemNativeStorageDocument[BloemNativeStorageSource](reg, scope, "POST", "/catalog/installations/{installation_id}/upgrade", "catalogUpgrade", "Download and verify a catalog upgrade, retaining configuration and library.", 200, bloemNativeStorageJSONBody[BloemNativeStorageUpgradeBody](reg), false)
		bloemNativeStorageDocument[BloemNativeStorageConfiguration](reg, scope, "PUT", "/sources/{source_key}/configuration", "replaceConfiguration", "Replace complete configuration only while the source namespace is empty.", 200, bloemNativeStorageJSONBody[BloemNativeStorageConfigurationBody](reg), false)
		bloemNativeStorageDocument[BloemNativeStorageConfiguration](reg, scope, "POST", "/sources/{source_key}/credentials", "rotateCredentials", "Replace only the S3 access key, secret key and session token of an enabled source, including one a library uses. Credentials are never returned.", 200, bloemNativeStorageJSONBody[BloemNativeStorageCredentialsBody](reg), false)
		bloemNativeStorageDocument[BloemNativeStorageSource](reg, scope, "POST", "/installations/{installation_id}/upgrade", "upgrade", "Install a newer approved artifact of the same plugin; the source, its configuration, library and catalog are kept.", 200, bloemNativeStorageMultipart[BloemNativeStorageUpgradeBody](reg), false)
		bloemNativeStorageDocument[BloemNativeStorageRemoval](reg, scope, "POST", "/installations/{installation_id}/disable", "disable", "Disable and detach the source; its library, catalog and reading progress are retained.", 200, bloemNativeStorageJSONBody[BloemNativeStorageRemoveBody](reg), false)
		bloemNativeStorageDocument[BloemNativeStorageS3Buckets](reg, scope, "POST", "/s3/buckets", "listS3Buckets", "List the buckets the supplied S3 credentials can see, for choosing a new source. Stateless: the credentials are used for this request only and never stored, logged or returned. The endpoint is checked against server-side request forgery and the connection is pinned to the checked addresses.", 200, bloemNativeStorageS3JSONBody[BloemNativeStorageS3BucketsBody](reg), false)
		bloemNativeStorageDocument[BloemNativeStorageS3Folders](reg, scope, "POST", "/s3/folders", "listS3Folders", "List one page (at most 200) of the immediate child folders of a prefix in a bucket, for choosing a new source's prefix. Files are never returned. Stateless, with the same endpoint guard and credential handling as the bucket listing.", 200, bloemNativeStorageS3JSONBody[BloemNativeStorageS3FoldersBody](reg), false)
		bloemNativeStorageDocument[BloemNativeStorageRemoval](reg, scope, "DELETE", "/installations/{installation_id}", "uninstall", "Uninstall and detach the source; its library, catalog and reading progress are retained.", 200, bloemNativeStorageJSONBody[BloemNativeStorageRemoveBody](reg), false)
	}
	bloemNativeStorageDocumentSchemas(reg.api.OpenAPI().Components.Schemas)
}

// UUID SchemaProvider drops pointer nullability; fix only these document-local
// properties. Configuration maps also require nonnull object values, unlike
// generic map schemas. Runtime JSON validation remains in the chi handler.
func bloemNativeStorageDocumentSchemas(schemas huma.Registry) {
	for typ, fields := range map[reflect.Type][]string{
		reflect.TypeFor[nativestorage.SourceView]():                      {"organization_id", "installation_id"},
		reflect.TypeFor[nativestorage.SourcePage]():                      {"next_after"},
		reflect.TypeFor[BloemNativeStorageInstallBody]():                 {"source_key", "expected_revision"},
		reflect.TypeFor[BloemNativeStoragePlatformInstallBody]():         {"source_key", "expected_revision", "organization_id"},
		reflect.TypeFor[BloemNativeStoragePlatformDiscoverInstallBody](): {"organization_id"},
	} {
		schema := schemas.Schema(typ, false, "")
		for _, name := range fields {
			schema.Properties[name].Nullable = true
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[BloemNativeStorageInstallBody](), reflect.TypeFor[BloemNativeStoragePlatformInstallBody](), reflect.TypeFor[BloemNativeStorageDiscoverInstallBody](), reflect.TypeFor[BloemNativeStoragePlatformDiscoverInstallBody](), reflect.TypeFor[BloemNativeStorageConfigurationBody]()} {
		schema := schemas.Schema(typ, false, "")
		schema.Properties["config"] = &huma.Schema{Type: "object", WriteOnly: true, AdditionalProperties: &huma.Schema{Type: "object", AdditionalProperties: true}, Description: "Complete encrypted provider configuration. Each keyed value must be an object; empty configuration is valid."}
	}
}
