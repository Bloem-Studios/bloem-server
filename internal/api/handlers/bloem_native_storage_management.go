package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	nativeStorageUnavailableCode = "native_storage_unavailable"
	nativeStorageJSONLimit       = 1 << 20
	nativeStorageBinaryLimit     = 256 << 20
	nativeStorageMultipartLimit  = 258 << 20
	// Even an empty ConfigEntry needs at least four serialized bytes.
	nativeStorageConfigEntryLimit = nativeStorageJSONLimit / 4
)

// BloemNativeStorageManagementHandler uses the retained domain authority and
// lifecycle transactions. It does not create queues, coordinators or grants.
type BloemNativeStorageManagementHandler struct {
	Sources *nativestorage.SourceManagement
	// Registry is the same startup registry used by Sources; only its sanitized
	// immutable artifact projection is used here.
	Registry *plugins.NativeStorageRegistry
	scope    auth.AdminScope
	// Capabilities is the actual router composition, not artifact/backend certification.
	Capabilities interface{ NativeStorageReady(context.Context) bool }
}

func NewBloemNativeStorageManagementHandler(s *nativestorage.SourceManagement) *BloemNativeStorageManagementHandler {
	return &BloemNativeStorageManagementHandler{Sources: s}
}

// ForAdminScope makes a route-local copy; scope selection is never a grant.
// The registrar must mount it after the existing AdminContextMiddleware.Require.
func (h *BloemNativeStorageManagementHandler) ForAdminScope(scope auth.AdminScope) *BloemNativeStorageManagementHandler {
	if h == nil {
		return &BloemNativeStorageManagementHandler{scope: scope}
	}
	copy := *h
	copy.scope = scope
	return &copy
}

func (h *BloemNativeStorageManagementHandler) actor(w http.ResponseWriter, r *http.Request) (auth.AdminContextClaims, bool) {
	w.Header().Set("Cache-Control", "no-store")
	claims, ok := middleware.GetAdminContextClaims(r.Context())
	if !ok || claims.AccountID <= 0 || claims.AccountIncarnationID == uuid.Nil || claims.SessionID == "" {
		writeError(w, 401, "tenant_session_required", "Valid administrative context required")
		return auth.AdminContextClaims{}, false
	}
	scope := auth.AdminScope("")
	if h != nil {
		scope = h.scope
	}
	switch scope {
	case auth.AdminScopePlatform:
		if claims.Scope != scope {
			writeError(w, 403, "insufficient_platform_authority", "Platform administrator authority required")
			return auth.AdminContextClaims{}, false
		}
	case auth.AdminScopeOrganization:
		if _, ok := requireBloemOrganizationContext(w, r); !ok {
			return auth.AdminContextClaims{}, false
		}
	default:
		writeError(w, 403, "insufficient_platform_authority", "Administrative route scope required")
		return auth.AdminContextClaims{}, false
	}
	return claims, true
}

func nativeStorageInvalid() error  { return &catalog.NativeOnboardingError{Code: "invalid_request"} }
func nativeStorageTooLarge() error { return &catalog.NativeOnboardingError{Code: "request_too_large"} }

// NativeStorageAPIError maps only fixed typed public codes. Internal causes and
// arbitrary producer text are never part of an HTTP response.
func NativeStorageAPIError(err error) *APIError {
	if err == nil {
		return nil
	}
	result := &APIError{Status: 503, Code: nativeStorageUnavailableCode, Message: "Native storage unavailable"}
	var unknown *catalog.MutationOutcomeUnknown
	if errors.As(err, &unknown) && unknown != nil {
		result.Code = "mutation_outcome_unknown"
		result.Message = "Mutation outcome requires reconciliation"
		return result
	}
	var typed *catalog.NativeOnboardingError
	if !errors.As(err, &typed) || typed == nil {
		return result
	}
	fixed := map[string]struct {
		status  int
		message string
	}{
		"invalid_request":                    {400, "Invalid native storage request"},
		"request_too_large":                  {413, "Native storage request exceeds the size limit"},
		"authorization_state_stale":          {401, "Tenant authorization state is stale"},
		"not_found":                          {404, "Resource not found"},
		"artifact_rejected":                  {422, "Native storage artifact rejected"},
		"revision_conflict":                  {409, "Revision changed; reload and retry"},
		"source_attached":                    {409, "Source is already attached"},
		"configuration_namespace_unverified": {409, "Configuration namespace replacement is unsupported"},
		"retained_namespace_unverified":      {409, "Retained namespace reinstallation is unsupported"},
		"native_mode_required":               {409, "Native library mode required"},
		"native_library_required":            {409, "Native library required"},
		"native_library_not_initialized":     {409, "Native library initialization required"},
		"binding_conflict":                   {409, "Native library binding conflicts with current state"},
		"native_library_deleting":            {409, "Native library is deleting"},
		"native_library_delete_unsupported":  {409, "Native library deletion is unsupported"},
		"native_local_operation_unsupported": {409, "Local operations on native libraries are unsupported"},
		"native_repair_unsupported":          {409, "Native library repair is unsupported"},
		"initialization_incomplete":          {503, "Native library initialization is incomplete"},
		nativeStorageUnavailableCode:         {503, "Native storage unavailable"},
	}
	if mapping, ok := fixed[typed.Code]; ok {
		result.Status = mapping.status
		result.Code = typed.Code
		result.Message = mapping.message
	}
	return result
}

func writeNativeStorageError(w http.ResponseWriter, err error, sourceOnly bool) {
	apiErr := NativeStorageAPIError(err)
	if apiErr == nil {
		apiErr = NativeStorageAPIError(&catalog.NativeOnboardingError{Code: nativeStorageUnavailableCode})
	}
	body := map[string]any{"error": apiErr.Code, "message": apiErr.Message}
	var typed *catalog.NativeOnboardingError
	if apiErr.Code == "revision_conflict" && errors.As(err, &typed) && typed != nil {
		if typed.CurrentSourceRevision != nil && *typed.CurrentSourceRevision > 0 {
			field := "current_source_revision"
			if sourceOnly {
				field = "current_revision"
			}
			body[field] = *typed.CurrentSourceRevision
		}
	}
	var unknown *catalog.MutationOutcomeUnknown
	if apiErr.Code == "mutation_outcome_unknown" && errors.As(err, &unknown) && unknown != nil {
		// These identifiers are allocated by the domain only after target authority.
		if unknown.OperationID != uuid.Nil {
			body["operation_id"] = unknown.OperationID
		}
		switch unknown.Operation {
		case "install", "upgrade", "configuration", "disable", "uninstall":
			body["operation"] = unknown.Operation
		}
		if unknown.SourceKey != nil && *unknown.SourceKey != uuid.Nil {
			body["source_key"] = *unknown.SourceKey
		}
	}
	nativeStorageWrite(w, apiErr.Status, body)
}

func nativeStorageWrite(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, body)
}

func nativeStorageText(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

// JSON is buffered only after the byte cap. Token validation catches duplicate
// keys at every nesting level, including escaped keys; exact struct tags reject
// encoding/json's otherwise case-insensitive field matching.
func nativeStorageJSON(w http.ResponseWriter, r *http.Request, target any) (map[string]json.RawMessage, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, nativeStorageInvalid()
	}
	if r.ContentLength > nativeStorageJSONLimit {
		return nil, nativeStorageTooLarge()
	}
	if r.Body == nil {
		return nil, nativeStorageInvalid()
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, nativeStorageJSONLimit))
	if err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			return nil, nativeStorageTooLarge()
		}
		return nil, nativeStorageInvalid()
	}
	return nativeStorageDecodeJSON(data, target)
}

func nativeStorageDecodeJSON(data []byte, target any) (map[string]json.RawMessage, error) {
	if len(data) > nativeStorageJSONLimit {
		return nil, nativeStorageTooLarge()
	}
	if !utf8.Valid(data) || !nativeStorageUnicodeEscapes(data) {
		return nil, nativeStorageInvalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, nativeStorageInvalid()
	}
	if err = nativeStorageJSONMembers(decoder, 0, '}'); err != nil {
		return nil, nativeStorageInvalid()
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, nativeStorageInvalid()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, nativeStorageInvalid()
	}
	valueType := reflect.TypeOf(target)
	if valueType == nil || valueType.Kind() != reflect.Pointer || valueType.Elem().Kind() != reflect.Struct {
		return nil, nativeStorageInvalid()
	}
	allowed := map[string]bool{}
	for _, field := range reflect.VisibleFields(valueType.Elem()) {
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			allowed[name] = true
		}
	}
	for key := range fields {
		if !allowed[key] {
			return nil, nativeStorageInvalid()
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return nil, nativeStorageInvalid()
	}
	return fields, nil
}

func nativeStorageJSONMembers(decoder *json.Decoder, depth int, end json.Delim) error {
	if depth > 64 {
		return nativeStorageInvalid()
	}
	seen := map[string]bool{}
	for decoder.More() {
		if end == '}' {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || seen[key] {
				return nativeStorageInvalid()
			}
			seen[key] = true
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			close := json.Delim('}')
			if delimiter == '[' {
				close = ']'
			} else if delimiter != '{' {
				return nativeStorageInvalid()
			}
			if err = nativeStorageJSONMembers(decoder, depth+1, close); err != nil {
				return err
			}
		}
	}
	token, err := decoder.Token()
	if err != nil || token != end {
		return nativeStorageInvalid()
	}
	return nil
}

// encoding/json replaces isolated UTF-16 surrogates. Reject those instead of
// changing an opaque source/config identifier silently.
func nativeStorageUnicodeEscapes(data []byte) bool {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		value, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func nativeStorageConfiguration(config map[string]map[string]any) error {
	if config == nil {
		return nativeStorageInvalid()
	}
	if len(config) > nativeStorageConfigEntryLimit {
		return nativeStorageTooLarge()
	}
	request := &publicv1.ConfigureRequest{}
	for key, value := range config {
		if !nativeStorageText(key, 1024) || value == nil {
			return nativeStorageInvalid()
		}
		converted, err := structpb.NewStruct(value)
		if err != nil {
			return nativeStorageInvalid()
		}
		request.Config = append(request.Config, &publicv1.ConfigEntry{Key: key, Value: converted})
	}
	if proto.Size(request) > nativeStorageJSONLimit {
		return nativeStorageTooLarge()
	}
	return nil
}

func nativeStorageInstallBody(w http.ResponseWriter, r *http.Request) (nativestorage.InstallCommand, map[string]json.RawMessage, error) {
	request, binary, err := nativeStorageMultipart(w, r)
	if err != nil {
		return nativestorage.InstallCommand{}, nil, err
	}
	var cmd nativestorage.InstallCommand
	fields, err := nativeStorageDecodeJSON(request, &cmd)
	if err != nil {
		return nativestorage.InstallCommand{}, nil, err
	}
	cmd.Binary = binary
	return cmd, fields, nil
}

func nativeStorageUpgradeBody(w http.ResponseWriter, r *http.Request) (nativestorage.UpgradeCommand, error) {
	request, binary, err := nativeStorageMultipart(w, r)
	if err != nil {
		return nativestorage.UpgradeCommand{}, err
	}
	var cmd nativestorage.UpgradeCommand
	fields, err := nativeStorageDecodeJSON(request, &cmd)
	if err != nil {
		return nativestorage.UpgradeCommand{}, err
	}
	for _, field := range []string{"artifact_key", "source_key", "expected_revision"} {
		if raw, present := fields[field]; !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nativestorage.UpgradeCommand{}, nativeStorageInvalid()
		}
	}
	if !nativeStorageText(cmd.ArtifactKey, 1024) || cmd.SourceKey == uuid.Nil || cmd.ExpectedRevision <= 0 {
		return nativestorage.UpgradeCommand{}, nativeStorageInvalid()
	}
	cmd.Binary = binary
	return cmd, nil
}

// nativeStorageMultipart reads exactly a request JSON part and a nonempty
// binary part within the upload limits.
func nativeStorageMultipart(w http.ResponseWriter, r *http.Request) ([]byte, []byte, error) {
	fail := func(err error) ([]byte, []byte, error) {
		return nil, nil, err
	}
	if r.ContentLength > nativeStorageMultipartLimit {
		return fail(nativeStorageTooLarge())
	}
	if r.Body == nil {
		return fail(nativeStorageInvalid())
	}
	r.Body = http.MaxBytesReader(w, r.Body, nativeStorageMultipartLimit)
	reader, err := r.MultipartReader()
	if err != nil {
		return fail(nativeStorageInvalid())
	}
	var request, binary []byte
	seen := map[string]bool{}
	for {
		// NextRawPart avoids transparent transfer-encoding transformations.
		part, err := reader.NextRawPart()
		if err == io.EOF { //nolint:errorlint // Only exact EOF marks the final boundary; wrapped EOF can report truncated multipart.
			break
		}
		if err != nil {
			var size *http.MaxBytesError
			if errors.As(err, &size) {
				return fail(nativeStorageTooLarge())
			}
			return fail(nativeStorageInvalid())
		}
		name := part.FormName()
		if (name != "request" && name != "binary") || seen[name] || part.Header.Get("Content-Transfer-Encoding") != "" {
			return fail(nativeStorageInvalid())
		}
		seen[name] = true
		limit := int64(nativeStorageJSONLimit)
		if name == "binary" {
			limit = nativeStorageBinaryLimit
		}
		data, err := io.ReadAll(io.LimitReader(part, limit+1))
		if err != nil {
			var size *http.MaxBytesError
			if errors.As(err, &size) {
				return fail(nativeStorageTooLarge())
			}
			return fail(nativeStorageInvalid())
		}
		if int64(len(data)) > limit {
			return fail(nativeStorageTooLarge())
		}
		part.Close()
		if name == "request" {
			request = data
		} else {
			binary = data
		}
	}
	if len(seen) != 2 || len(binary) == 0 {
		return fail(nativeStorageInvalid())
	}
	// Multipart EOF marks the final boundary, not raw HTTP body completion.
	// Drain the same capped body: parser read-ahead has already spent its byte
	// budget. io.Discard uses a fixed-size buffer, including for the epilogue.
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			return fail(nativeStorageTooLarge())
		}
		return fail(nativeStorageInvalid())
	}
	return request, binary, nil
}

func nativeStorageQuery(r *http.Request, allowed ...string) (url.Values, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, nativeStorageInvalid()
	}
	keys := map[string]bool{}
	for _, key := range allowed {
		keys[key] = true
	}
	for key, entries := range values {
		if !keys[key] || len(entries) != 1 || entries[0] == "" {
			return nil, nativeStorageInvalid()
		}
	}
	return values, nil
}
func nativeStorageNoBody(r *http.Request) error {
	if r.Body == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if len(data) > 0 || err != nil {
		return nativeStorageInvalid()
	}
	return nil
}
func nativeStoragePositiveID(value string) (int, error) {
	if value == "" {
		return 0, nativeStorageInvalid()
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, nativeStorageInvalid()
		}
	}
	id, err := strconv.ParseInt(value, 10, 32)
	if err != nil || id <= 0 {
		return 0, nativeStorageInvalid()
	}
	return int(id), nil
}
func nativeStorageUUID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, nativeStorageInvalid()
	}
	return id, nil
}
func nativeStoragePage(r *http.Request) (url.Values, int, error) {
	values, err := nativeStorageQuery(r, "after", "limit")
	if err != nil {
		return nil, 0, err
	}
	limit := 50
	if value := values.Get("limit"); value != "" {
		limit, err = nativeStoragePositiveID(value)
		if err != nil || limit > 100 {
			return nil, 0, nativeStorageInvalid()
		}
	}
	if err = nativeStorageNoBody(r); err != nil {
		return nil, 0, err
	}
	return values, limit, nil
}
func nativeStoragePathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	key, err := nativeStorageUUID(chi.URLParam(r, name))
	if err != nil {
		writeNativeStorageError(w, &catalog.NativeOnboardingError{Code: "not_found"}, false)
		return uuid.Nil, false
	}
	return key, true
}
func nativeStoragePathID(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	value := chi.URLParam(r, name)
	id, err := nativeStoragePositiveID(value)
	if name == "installation_id" {
		parsed, e := strconv.ParseInt(value, 10, strconv.IntSize)
		if e == nil && parsed > 0 && !strings.HasPrefix(value, "+") {
			id = int(parsed)
			err = nil
		} else {
			err = nativeStorageInvalid()
		}
	}
	if err != nil {
		writeNativeStorageError(w, &catalog.NativeOnboardingError{Code: "not_found"}, false)
		return 0, false
	}
	return id, true
}

func (h *BloemNativeStorageManagementHandler) readRequest(w http.ResponseWriter, r *http.Request) bool {
	if _, err := nativeStorageQuery(r); err != nil {
		writeNativeStorageError(w, err, false)
		return false
	}
	if err := nativeStorageNoBody(r); err != nil {
		writeNativeStorageError(w, err, false)
		return false
	}
	return true
}
func (h *BloemNativeStorageManagementHandler) command(w http.ResponseWriter, r *http.Request, target any) (map[string]json.RawMessage, bool) {
	if _, err := nativeStorageQuery(r); err != nil {
		writeNativeStorageError(w, err, false)
		return nil, false
	}
	fields, err := nativeStorageJSON(w, r, target)
	if err != nil {
		writeNativeStorageError(w, err, false)
		return nil, false
	}
	return fields, true
}
func (h *BloemNativeStorageManagementHandler) sourceAvailable(w http.ResponseWriter) bool {
	if h == nil || h.Sources == nil {
		writeNativeStorageError(w, &catalog.NativeOnboardingError{Code: nativeStorageUnavailableCode}, false)
		return false
	}
	return true
}

func nativeStorageInstallRequest(cmd *nativestorage.InstallCommand, fields map[string]json.RawMessage, scope auth.AdminScope) error {
	if scope == auth.AdminScopeOrganization {
		if _, present := fields["organization_id"]; present {
			return nativeStorageInvalid()
		}
	}
	for _, field := range []string{"artifact_key", "provider_source_id", "root_entry_id", "enabled", "config"} {
		raw, present := fields[field]
		if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nativeStorageInvalid()
		}
	}
	if !nativeStorageText(cmd.ArtifactKey, 1024) || !nativeStorageText(cmd.ProviderSourceID, 1024) || !nativeStorageText(cmd.RootEntryID, 1024) {
		return nativeStorageInvalid()
	}
	if cmd.OrganizationID != nil && *cmd.OrganizationID == uuid.Nil {
		return nativeStorageInvalid()
	}
	if cmd.SourceKey == nil {
		if cmd.ExpectedRevision != nil {
			return nativeStorageInvalid()
		}
	} else if *cmd.SourceKey == uuid.Nil || cmd.ExpectedRevision == nil || *cmd.ExpectedRevision <= 0 {
		return nativeStorageInvalid()
	}
	return nativeStorageConfiguration(cmd.Config)
}

// nativeStorageCapabilities reports which source operations the composed
// router supports. Libraries use storage sources through the ordinary library
// API, so no library operations are listed here.
func (h *BloemNativeStorageManagementHandler) nativeStorageCapabilities(ctx context.Context) map[string]any {
	ready := h != nil && h.Capabilities != nil && h.Capabilities.NativeStorageReady(ctx)
	return map[string]any{"schema": 2, "source_management": ready, "approved_artifact_install": ready,
		"configuration_replace_unbound": ready, "disable": ready, "uninstall": ready, "upgrade": ready,
		"retained_namespace_reinstall": false, "enable": false, "backend_verified": false}
}

func (h *BloemNativeStorageManagementHandler) HandleCapabilities(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	if !h.readRequest(w, r) {
		return
	}
	nativeStorageWrite(w, 200, h.nativeStorageCapabilities(r.Context()))
}
func (h *BloemNativeStorageManagementHandler) HandleArtifacts(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r); !ok {
		return
	}
	if !h.readRequest(w, r) {
		return
	}
	nativeStorageWrite(w, 200, nativeStorageArtifactsResponse{h.Registry.ApprovedArtifacts()})
}
func (h *BloemNativeStorageManagementHandler) HandleListSources(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	values, limit, err := nativeStoragePage(r)
	if err != nil {
		writeNativeStorageError(w, err, false)
		return
	}
	var after *uuid.UUID
	if value := values.Get("after"); value != "" {
		id, e := nativeStorageUUID(value)
		if e != nil {
			writeNativeStorageError(w, e, false)
			return
		}
		after = &id
	}
	if !h.sourceAvailable(w) {
		return
	}
	page, err := h.Sources.ListSources(r.Context(), actor, after, limit)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, 200, page)
}
func (h *BloemNativeStorageManagementHandler) HandleGetSource(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	if !h.readRequest(w, r) {
		return
	}
	key, ok := nativeStoragePathUUID(w, r, "source_key")
	if !ok || !h.sourceAvailable(w) {
		return
	}
	source, err := h.Sources.GetSource(r.Context(), actor, key)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, 200, nativeStorageSourceResponse{source})
}
func (h *BloemNativeStorageManagementHandler) HandleInstall(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	if _, err := nativeStorageQuery(r); err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	cmd, fields, err := nativeStorageInstallBody(w, r)
	if err == nil {
		err = nativeStorageInstallRequest(&cmd, fields, actor.Scope)
	}
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	if !h.sourceAvailable(w) {
		return
	}
	source, err := h.Sources.Install(r.Context(), actor, cmd)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, 201, nativeStorageSourceResponse{source})
}

// HandleUpgrade installs a newer approved artifact on an installation, keeping
// its source, configuration and library.
func (h *BloemNativeStorageManagementHandler) HandleUpgrade(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := nativeStoragePathID(w, r, "installation_id")
	if !ok {
		return
	}
	if _, err := nativeStorageQuery(r); err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	cmd, err := nativeStorageUpgradeBody(w, r)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	if !h.sourceAvailable(w) {
		return
	}
	source, err := h.Sources.Upgrade(r.Context(), actor, id, cmd)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, 200, nativeStorageSourceResponse{source})
}
func (h *BloemNativeStorageManagementHandler) HandleReplaceConfiguration(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	key, ok := nativeStoragePathUUID(w, r, "source_key")
	if !ok {
		return
	}
	var cmd nativeStorageConfigurationCommand
	if _, ok = h.command(w, r, &cmd); !ok {
		return
	}
	err := nativeStorageConfiguration(cmd.Config)
	if cmd.ExpectedRevision <= 0 {
		err = nativeStorageInvalid()
	}
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	if !h.sourceAvailable(w) {
		return
	}
	revision, err := h.Sources.ReplaceConfiguration(r.Context(), actor, key, cmd.ExpectedRevision, cmd.Config)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, 200, nativeStorageConfigurationResponse{key, revision})
}
func (h *BloemNativeStorageManagementHandler) HandleDisable(w http.ResponseWriter, r *http.Request) {
	h.remove(w, r, false)
}
func (h *BloemNativeStorageManagementHandler) HandleUninstall(w http.ResponseWriter, r *http.Request) {
	h.remove(w, r, true)
}
func (h *BloemNativeStorageManagementHandler) remove(w http.ResponseWriter, r *http.Request, uninstall bool) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := nativeStoragePathID(w, r, "installation_id")
	if !ok {
		return
	}
	var cmd nativeStorageRemoveCommand
	if _, ok = h.command(w, r, &cmd); !ok {
		return
	}
	if cmd.SourceKey == uuid.Nil || cmd.ExpectedRevision <= 0 {
		writeNativeStorageError(w, nativeStorageInvalid(), true)
		return
	}
	if !h.sourceAvailable(w) {
		return
	}
	if err := h.Sources.Remove(r.Context(), actor, id, cmd.SourceKey, cmd.ExpectedRevision, uninstall); err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	state := "disabled_detached"
	if uninstall {
		state = "uninstalled_detached"
	}
	nativeStorageWrite(w, 200, nativeStorageRemoveResponse{id, state, true})
}
