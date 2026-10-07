package apiv2

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
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
type BloemNativeStorageCapabilities struct {
	Schema                      int  `json:"schema" enum:"2"`
	SourceManagement            bool `json:"source_management"`
	ApprovedArtifactInstall     bool `json:"approved_artifact_install"`
	ConfigurationReplaceUnbound bool `json:"configuration_replace_unbound"`
	Disable                     bool `json:"disable"`
	Uninstall                   bool `json:"uninstall"`
	RetainedNamespaceReinstall  bool `json:"retained_namespace_reinstall" enum:"false"`
	Enable                      bool `json:"enable" enum:"false"`
	BackendVerified             bool `json:"backend_verified" enum:"false"`
}

type BloemNativeStorageArtifacts struct {
	Artifacts []plugins.NativeStorageArtifactView `json:"artifacts" nullable:"false"`
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
	Error                 string     `json:"error"`
	Message               string     `json:"message"`
	CurrentRevision       *int64     `json:"current_revision,omitempty" nullable:"false"`
	CurrentSourceRevision *int64     `json:"current_source_revision,omitempty" nullable:"false"`
	OperationID           *uuid.UUID `json:"operation_id,omitempty" nullable:"false"`
	Operation             *string    `json:"operation,omitempty" nullable:"false" enum:"install,configuration,disable,uninstall"`
	SourceKey             *uuid.UUID `json:"source_key,omitempty" nullable:"false"`
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

func bloemNativeStorageDocument[T any](reg *Registry, scope, method, path, id, summary string, status int, body *huma.RequestBody, list bool) {
	security, label := bloemPlatformContextSecurity, "Platform"
	if scope == "organization" {
		security, label = bloemOrganizationContextSecurity, "Organization"
	}
	op := bloemChiDocumentOp(reg, method, "/admin/"+scope+"/native-storage"+path, id+"Bloem"+label+"NativeStorage", "native-storage", summary, security, status)
	op.Description = "Experimental native EPUB/PDF storage sources. A library uses a source as its storage location through the ordinary library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Native-storage mutations are outside shared lifecycle idempotency; reconcile unknown outcomes instead of automatically replaying."
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
	bloemDocumentErrors[BloemNativeStorageError](reg, &op, map[int]string{
		400: "invalid_request: malformed or unsupported body/query/fields.",
		404: "not_found: invalid path identity, absent resource or hidden authority.",
		503: "native_storage_unavailable: dependencies or state cannot establish readiness. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback.",
	})
	if method != "GET" {
		bloemDocumentErrors[BloemNativeStorageError](reg, &op, map[int]string{
			409: "revision_conflict returns current_revision. Other fixed codes include source_attached, configuration_namespace_unverified and retained_namespace_unverified.",
			413: "request_too_large: JSON, binary or total multipart limit exceeded.",
		})
	}
	if path == "/installations" {
		bloemDocumentErrors[BloemNativeStorageError](reg, &op, map[int]string{422: "artifact_rejected: uploaded bytes do not match an accepted host-approved artifact."})
	}
	op.Responses[strconv.Itoa(status)] = &huma.Response{Description: summary}
	for _, response := range op.Responses {
		response.Headers = map[string]*huma.Header{"Cache-Control": {Description: "Native management handler responses are not cacheable.", Schema: &huma.Schema{Type: "string", Enum: []any{"no-store"}}}}
	}
	registerBloemChiDocument[struct{}, bloemNativeStorageOutput[T]](reg, op)
}

func registerBloemNativeStorageDocument(reg *Registry) {
	for _, scope := range []string{"platform", "organization"} {
		bloemNativeStorageDocument[BloemNativeStorageCapabilities](reg, scope, "GET", "/capabilities", "getCapabilities", "Read composition-dependent flags; backend verification remains false.", 200, nil, false)
		bloemNativeStorageDocument[BloemNativeStorageArtifacts](reg, scope, "GET", "/artifacts", "listArtifacts", "List sanitized immutable host-approved artifacts.", 200, nil, false)
		bloemNativeStorageDocument[nativestorage.SourcePage](reg, scope, "GET", "/sources", "listSources", "List visible sources and their current configuration revisions.", 200, nil, true)
		bloemNativeStorageDocument[BloemNativeStorageSource](reg, scope, "GET", "/sources/{source_key}", "getSource", "Read a visible source without configuration secrets.", 200, nil, false)
		install := bloemNativeStorageMultipart[BloemNativeStorageInstallBody](reg)
		if scope == "platform" {
			install = bloemNativeStorageMultipart[BloemNativeStoragePlatformInstallBody](reg)
		}
		bloemNativeStorageDocument[BloemNativeStorageSource](reg, scope, "POST", "/installations", "install", "Install an approved artifact and source without creating a library or scan.", 201, install, false)
		bloemNativeStorageDocument[BloemNativeStorageConfiguration](reg, scope, "PUT", "/sources/{source_key}/configuration", "replaceConfiguration", "Replace complete configuration only while the source namespace is empty.", 200, bloemNativeStorageJSONBody[BloemNativeStorageConfigurationBody](reg), false)
		bloemNativeStorageDocument[BloemNativeStorageRemoval](reg, scope, "POST", "/installations/{installation_id}/disable", "disable", "Disable and detach the source; its library, catalog and reading progress are retained.", 200, bloemNativeStorageJSONBody[BloemNativeStorageRemoveBody](reg), false)
		bloemNativeStorageDocument[BloemNativeStorageRemoval](reg, scope, "DELETE", "/installations/{installation_id}", "uninstall", "Uninstall and detach the source; its library, catalog and reading progress are retained.", 200, bloemNativeStorageJSONBody[BloemNativeStorageRemoveBody](reg), false)
	}
	bloemNativeStorageDocumentSchemas(reg.api.OpenAPI().Components.Schemas)
}

// UUID SchemaProvider drops pointer nullability; fix only these document-local
// properties. Configuration maps also require nonnull object values, unlike
// generic map schemas. Runtime JSON validation remains in the chi handler.
func bloemNativeStorageDocumentSchemas(schemas huma.Registry) {
	for typ, fields := range map[reflect.Type][]string{
		reflect.TypeFor[nativestorage.SourceView]():              {"organization_id", "installation_id"},
		reflect.TypeFor[nativestorage.SourcePage]():              {"next_after"},
		reflect.TypeFor[BloemNativeStorageInstallBody]():         {"source_key", "expected_revision"},
		reflect.TypeFor[BloemNativeStoragePlatformInstallBody](): {"source_key", "expected_revision", "organization_id"},
	} {
		schema := schemas.Schema(typ, false, "")
		for _, name := range fields {
			schema.Properties[name].Nullable = true
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[BloemNativeStorageInstallBody](), reflect.TypeFor[BloemNativeStoragePlatformInstallBody](), reflect.TypeFor[BloemNativeStorageConfigurationBody]()} {
		schema := schemas.Schema(typ, false, "")
		schema.Properties["config"] = &huma.Schema{Type: "object", WriteOnly: true, AdditionalProperties: &huma.Schema{Type: "object", AdditionalProperties: true}, Description: "Complete encrypted provider configuration. Each keyed value must be an object; empty configuration is valid."}
	}
}
