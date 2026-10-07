package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/google/uuid"
)

type nativeUnreadBody struct{ t *testing.T }

func (b nativeUnreadBody) Read([]byte) (int, error) {
	b.t.Fatal("unauthenticated handler read the body")
	return 0, io.EOF
}
func (b nativeUnreadBody) Close() error { return nil }

func TestNativeOnboardingHTTPRequiresRetainedContextBeforeBody(t *testing.T) {
	h := NewBloemNativeStorageManagementHandler(nil, nil)
	cases := []struct {
		name string
		call http.HandlerFunc
	}{
		{"capabilities", h.HandleCapabilities}, {"artifacts", h.HandleArtifacts}, {"sources", h.HandleListSources},
		{"source", h.HandleGetSource}, {"install", h.HandleInstall}, {"configuration", h.HandleReplaceConfiguration},
		{"disable", h.HandleDisable}, {"uninstall", h.HandleUninstall}, {"bindings", h.HandleBindings}, {"bind", h.HandleBind},
		{"create", h.HandleCreateLibrary}, {"library", h.HandleGetLibrary}, {"creation", h.HandleGetLibraryByCreationKey},
		{"libraries", h.HandleListLibraries}, {"initialize", h.HandleInitializeLibrary}, {"scan", h.HandleScanLibrary},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/bloem/v1/admin/platform/native-storage/libraries", nil)
			r.Body = nativeUnreadBody{t}
			w := httptest.NewRecorder()
			tt.call(w, r)
			assertNativeError(t, w, http.StatusUnauthorized, "tenant_session_required")
		})
	}
}

func assertNativeError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) map[string]any {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d", w.Code, status)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("management response must be no-store")
	}
	var body map[string]any
	if json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatal("response is not bounded JSON")
	}
	if body["error"] != code {
		t.Fatalf("error=%v want=%s", body["error"], code)
	}
	if _, ok := body["message"].(string); !ok {
		t.Fatal("fixed message missing")
	}
	return body
}

func TestNativeOnboardingHTTPStrictJSON(t *testing.T) {
	type request struct {
		ExpectedRevision int64                     `json:"expected_revision"`
		Config           map[string]map[string]any `json:"config"`
	}
	cases := []struct {
		name, body string
		status     int
		code       string
	}{
		{"duplicate", `{"expected_revision":1,"expected_revision":2,"config":{}}`, 400, "invalid_request"},
		{"escaped_duplicate", `{"expected_revision":1,"expected_\u0072evision":2,"config":{}}`, 400, "invalid_request"},
		{"nested_duplicate", `{"expected_revision":1,"config":{"storage":{"secret":"a","secret":"b"}}}`, 400, "invalid_request"},
		{"unknown", `{"expected_revision":1,"config":{},"verified":true}`, 400, "invalid_request"},
		{"case_variant", `{"EXPECTED_REVISION":1,"config":{}}`, 400, "invalid_request"},
		{"trailing", `{"expected_revision":1,"config":{}} {}`, 400, "invalid_request"},
		{"array", `[{"expected_revision":1}]`, 400, "invalid_request"},
		{"null", `null`, 400, "invalid_request"},
		{"fraction", `{"expected_revision":1.5,"config":{}}`, 400, "invalid_request"},
		{"overflow", `{"expected_revision":9223372036854775808,"config":{}}`, 400, "invalid_request"},
		{"bad_utf8", "{\"expected_revision\":1,\"config\":{\"s\":{\"x\":\"" + string([]byte{0xff}) + "\"}}}", 400, "invalid_request"},
		{"unpaired_surrogate", `{"expected_revision":1,"config":{"s":{"x":"\ud800"}}}`, 400, "invalid_request"},
		{"oversize", strings.Repeat(" ", (1<<20)+1), 413, "request_too_large"},
		{"malformed", `{"expected_revision":`, 400, "invalid_request"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			var cmd request
			w := httptest.NewRecorder()
			_, err := nativeStorageJSON(w, r, &cmd)
			if err == nil {
				t.Fatal("invalid command accepted")
			}
			writeNativeStorageError(w, err, false, nil)
			assertNativeError(t, w, tt.status, tt.code)
			if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "verified") {
				t.Fatal("decoder echoed input")
			}
		})
	}
	r := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"expected_revision":9223372036854775807,"config":{"storage":{"x":"\ud83d\ude00","number":1}}}`))
	r.Header.Set("Content-Type", "application/json")
	var cmd request
	if _, err := nativeStorageJSON(httptest.NewRecorder(), r, &cmd); err != nil || cmd.ExpectedRevision != 9223372036854775807 {
		t.Fatal("valid exact int64/Unicode command rejected")
	}
}

func nativeMultipartRequest(t *testing.T, parts [][2]string) *http.Request {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for _, part := range parts {
		p, err := writer.CreateFormField(part[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.Write([]byte(part[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", &buffer)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	return r
}

func TestNativeOnboardingHTTPMultipartIsAtomicAndExact(t *testing.T) {
	const request = `{"artifact_key":"fixture","provider_source_id":"collection","root_entry_id":"root","enabled":true,"config":{}}`
	cases := []struct {
		name  string
		parts [][2]string
		code  string
	}{
		{"missing_binary", [][2]string{{"request", request}}, "invalid_request"},
		{"empty_binary", [][2]string{{"request", request}, {"binary", ""}}, "invalid_request"},
		{"duplicate_binary", [][2]string{{"request", request}, {"binary", "first"}, {"binary", "second"}}, "invalid_request"},
		{"duplicate_request", [][2]string{{"request", request}, {"request", request}, {"binary", "bin"}}, "invalid_request"},
		{"extra_part", [][2]string{{"request", request}, {"binary", "bin"}, {"secret", "do-not-echo"}}, "invalid_request"},
		{"unknown_field", [][2]string{{"request", `{"artifact_key":"fixture","verified":true}`}, {"binary", "bin"}}, "invalid_request"},
		{"oversize_request", [][2]string{{"request", strings.Repeat("x", (1<<20)+1)}, {"binary", "bin"}}, "request_too_large"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cmd, fields, err := nativeStorageInstallBody(httptest.NewRecorder(), nativeMultipartRequest(t, tt.parts))
			if err == nil {
				t.Fatal("partial or nonexact multipart accepted")
			}
			if len(cmd.Binary) != 0 || fields != nil {
				t.Fatal("invalid multipart retained partial command")
			}
			e := NativeStorageAPIError(err)
			if e.Code != tt.code {
				t.Fatalf("code=%s want=%s", e.Code, tt.code)
			}
		})
	}
	for _, parts := range [][][2]string{
		{{"request", request}, {"binary", "bin"}}, {{"binary", "bin"}, {"request", request}},
	} {
		cmd, fields, err := nativeStorageInstallBody(httptest.NewRecorder(), nativeMultipartRequest(t, parts))
		if err != nil || string(cmd.Binary) != "bin" || cmd.RootEntryID != "root" || len(fields) != 5 {
			t.Fatal("exact multipart rejected")
		}
	}
	r := nativeMultipartRequest(t, [][2]string{{"request", request}, {"binary", "bin"}})
	r.ContentLength = (258 << 20) + 1
	if _, _, err := nativeStorageInstallBody(httptest.NewRecorder(), r); NativeStorageAPIError(err).Code != "request_too_large" {
		t.Fatal("declared total limit not enforced")
	}
}

// nativeMultipartStream emits an epilogue directly into the caller's bounded
// read buffer. It never allocates storage proportional to the HTTP body.
type nativeMultipartStream struct {
	prefix          []byte
	remaining       int64
	maxRead         int
	consumed        int64
	readAhead       bool
	completionRead  bool
	completionError error
	errorWithData   bool
}

func (s *nativeMultipartStream) Read(p []byte) (int, error) {
	if s.maxRead > 0 && len(p) > s.maxRead {
		p = p[:s.maxRead]
	}
	n := copy(p, s.prefix)
	s.prefix = s.prefix[n:]
	tail := min(int64(len(p)-n), s.remaining)
	clear(p[n : n+int(tail)])
	if n > 0 && tail > 0 {
		s.readAhead = true
	}
	n += int(tail)
	s.remaining -= tail
	s.consumed += int64(n)
	if len(s.prefix) == 0 && s.remaining == 0 && (n == 0 || s.errorWithData) {
		s.completionRead = true
		if s.completionError != nil {
			return n, s.completionError
		}
		return n, io.EOF
	}
	return n, nil
}

func (*nativeMultipartStream) Close() error { return nil }

func TestNativeOnboardingHTTPMultipartRawBodyCompletion(t *testing.T) {
	const request = `{"artifact_key":"fixture","provider_source_id":"collection","root_entry_id":"root","enabled":true,"config":{}}`
	// Hand-derived wire requirement, independent of the decoder's limit value.
	const totalLimit int64 = 258 << 20
	privateError := errors.New("private completion I/O detail")
	cases := []struct {
		name          string
		epilogue      int64 // Negative values select an exact total body length.
		maxRead       int
		readAhead     bool
		completionErr error
		errorWithData bool
		status        int
		code          string
	}{
		{"unknown_length_oversized_epilogue", totalLimit + 4096, 0, true, nil, false, 413, "request_too_large"},
		{"unknown_length_just_over_total_limit", -(totalLimit + 1), 0, true, nil, false, 413, "request_too_large"},
		{"unknown_length_exact_total_limit", -totalLimit, 0, true, nil, false, 0, ""},
		{"no_epilogue", 0, 0, false, nil, false, 0, ""},
		{"small_buffered_epilogue", 128, 0, true, nil, false, 0, ""},
		{"fragmented_epilogue", 128, 1, false, nil, false, 0, ""},
		{"epilogue_beyond_parser_buffer", 8192, 0, true, nil, false, 0, ""},
		{"completion_io_error", 8192, 0, true, privateError, false, 400, "invalid_request"},
		{"buffered_completion_io_error", 128, 0, true, privateError, true, 400, "invalid_request"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := nativeMultipartRequest(t, [][2]string{{"request", request}, {"binary", "bin"}})
			prefix, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			epilogue := tt.epilogue
			if epilogue < 0 {
				epilogue = -epilogue - int64(len(prefix))
			}
			total := int64(len(prefix)) + epilogue
			stream := &nativeMultipartStream{
				prefix: prefix, remaining: epilogue, maxRead: tt.maxRead,
				completionError: tt.completionErr, errorWithData: tt.errorWithData,
			}
			r.Body = stream
			r.ContentLength = -1
			r.TransferEncoding = []string{"chunked"}
			w := httptest.NewRecorder()
			cmd, fields, err := nativeStorageInstallBody(w, r)
			if tt.status != 0 {
				if err == nil {
					t.Fatal("incomplete or oversized raw HTTP body returned an install command")
				}
				if !reflect.DeepEqual(cmd, nativestorage.InstallCommand{}) || fields != nil {
					t.Fatal("completion refusal retained a partial command")
				}
				writeNativeStorageError(w, err, false, nil)
				assertNativeError(t, w, tt.status, tt.code)
				if strings.Contains(w.Body.String(), privateError.Error()) {
					t.Fatal("completion I/O detail disclosed")
				}
			} else {
				if err != nil {
					t.Fatalf("valid within-limit multipart rejected: %v", err)
				}
				if string(cmd.Binary) != "bin" || cmd.RootEntryID != "root" || len(fields) != 5 {
					t.Fatal("raw body completion changed the valid command")
				}
				if stream.consumed != total || !stream.completionRead {
					t.Fatalf("command returned before raw completion: consumed=%d total=%d", stream.consumed, total)
				}
			}
			if stream.readAhead != tt.readAhead {
				t.Fatal("stream did not exercise the intended parser read-ahead")
			}
			if total > totalLimit && stream.consumed != totalLimit+1 {
				t.Fatalf("oversize drain consumed=%d want bounded probe=%d", stream.consumed, totalLimit+1)
			}
			if tt.completionErr != nil && !stream.completionRead {
				t.Fatal("test did not reach raw completion I/O failure")
			}
		})
	}
}

func TestNativeOnboardingHTTPSerializedConfigurationBounds(t *testing.T) {
	if err := nativeStorageConfiguration(map[string]map[string]any{"s": {"x": "ok", "n": 1.0, "nested": map[string]any{"yes": true}}}); err != nil {
		t.Fatal("valid structpb config rejected")
	}
	for _, config := range []map[string]map[string]any{nil, {"s": nil}, {"": {}}, {"s": {"x": make(chan int)}}} {
		if err := nativeStorageConfiguration(config); NativeStorageAPIError(err).Code != "invalid_request" {
			t.Fatal("invalid config accepted")
		}
	}
	// Numeric protobuf values use eight-byte doubles even when JSON sends 0.
	values := map[string]any{}
	for i := 0; i < 60000; i++ {
		values[fmt.Sprintf("x%05d", i)] = float64(0)
	}
	config := map[string]map[string]any{"storage": values}
	encoded, _ := json.Marshal(config)
	if len(encoded) > 1<<20 {
		t.Fatal("test must reach protobuf bound before JSON bound")
	}
	if err := nativeStorageConfiguration(config); err == nil || NativeStorageAPIError(err).Code != "request_too_large" {
		t.Fatal("actual serialized ConfigureRequest bound not enforced")
	}

}

func TestNativeOnboardingHTTPFixedErrorsAndRecoveryPrivacy(t *testing.T) {
	sourceRevision, libraryRevision := int64(4), int64(3)
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{errors.New("SQL private-secret /host/install/path"), 503, "native_storage_unavailable"},
		{&catalog.NativeOnboardingError{Code: "private-secret", Cause: errors.New("secret")}, 503, "native_storage_unavailable"},
		{&catalog.NativeOnboardingError{Code: "artifact_rejected", Cause: errors.New("approved-checksum")}, 422, "artifact_rejected"},
		{&catalog.NativeOnboardingError{Code: "revision_conflict", CurrentSourceRevision: &sourceRevision, CurrentLibraryRevision: &libraryRevision}, 409, "revision_conflict"},
		{&catalog.NativeOnboardingError{Code: "not_found", CurrentSourceRevision: &sourceRevision, Cause: errors.New("private-secret")}, 404, "not_found"},
		{&catalog.NativeOnboardingError{Code: "native_library_not_initialized"}, 409, "native_library_not_initialized"},
		{&catalog.NativeOnboardingError{Code: "authorization_state_stale"}, 401, "authorization_state_stale"},
	}
	for _, tt := range cases {
		w := httptest.NewRecorder()
		writeNativeStorageError(w, tt.err, false, nil)
		body := assertNativeError(t, w, tt.status, tt.code)
		if strings.Contains(w.Body.String(), "private-secret") || strings.Contains(w.Body.String(), "approved-checksum") || strings.Contains(w.Body.String(), "/host/") {
			t.Fatal("cause leaked")
		}
		if tt.code == "revision_conflict" {
			if body["current_source_revision"] != float64(4) || body["current_library_revision"] != float64(3) {
				t.Fatal("S/L revisions lost")
			}
		} else if _, ok := body["current_source_revision"]; ok {
			t.Fatal("revision disclosed outside conflict")
		}
	}
	w := httptest.NewRecorder()
	writeNativeStorageError(w, &catalog.NativeOnboardingError{Code: "revision_conflict", CurrentSourceRevision: &sourceRevision}, true, nil)
	body := assertNativeError(t, w, 409, "revision_conflict")
	if body["current_revision"] != float64(4) {
		t.Fatal("source-only legacy revision missing")
	}
	for _, operation := range []string{"create", "initialize", "bind", "scan", "install", "disable", "uninstall", "configuration"} {
		key, creation, operationID := uuid.New(), uuid.New(), uuid.New()
		run := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
		unknown := &catalog.MutationOutcomeUnknown{OperationID: operationID, CreationKey: creation, LibraryID: 7, SourceKey: &key, ScanRunID: &run, Operation: operation, Cause: errors.New("private-secret")}
		w := httptest.NewRecorder()
		writeNativeStorageError(w, unknown, false, nil)
		body := assertNativeError(t, w, 503, "mutation_outcome_unknown")
		if body["operation_id"] != operationID.String() || body["operation"] != operation || body["creation_key"] != creation.String() || body["source_key"] != key.String() || body["scan_run_id"] != run || body["library_id"] != float64(7) {
			t.Fatal("authorized recovery identifiers lost")
		}
		if strings.Contains(w.Body.String(), "private-secret") {
			t.Fatal("unknown cause leaked")
		}
	}
	incomplete := nativestorage.LibraryStatus{LibraryID: 7, CreationKey: uuid.New(), LibraryRevision: 1, State: "initialization_required"}
	w = httptest.NewRecorder()
	writeNativeStorageError(w, &catalog.NativeOnboardingError{Code: "initialization_incomplete", Cause: errors.New("secret")}, false, &incomplete)
	body = assertNativeError(t, w, 503, "initialization_incomplete")
	if body["library_id"] != float64(7) || body["state"] != "initialization_required" || body["library_revision"] != float64(1) {
		t.Fatal("incomplete initialization recovery lost")
	}
}

func TestNativeOnboardingHTTPCreateAndInstallScopeShape(t *testing.T) {
	cases := []struct {
		name, body   string
		organization bool
		valid        bool
		language     string
	}{
		{"default_language", `{"name":" Books "}`, false, true, "en"},
		{"explicit_language", `{"name":"Books","metadata_language":"nl"}`, false, true, "nl"},
		{"platform_target", `{"name":"Books","organization_id":null}`, false, true, "en"},
		{"organization_rejects_null_target", `{"name":"Books","organization_id":null}`, true, false, ""},
		{"missing_name", `{}`, false, false, ""},
		{"blank_name", `{"name":" "}`, false, false, ""},
		{"null_language", `{"name":"Books","metadata_language":null}`, false, false, ""},
		{"blank_language", `{"name":"Books","metadata_language":" "}`, false, false, ""},
		{"nil_target_uuid", `{"name":"Books","organization_id":"00000000-0000-0000-0000-000000000000"}`, false, false, ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var cmd nativestorage.LibraryCreateCommand
			fields, err := nativeStorageDecodeJSON([]byte(tt.body), &cmd)
			scope := auth.AdminScopePlatform
			if tt.organization {
				scope = auth.AdminScopeOrganization
			}
			if err == nil {
				err = nativeStorageCreateRequest(&cmd, fields, scope)
			}
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v want=%v", err == nil, tt.valid)
			}
			if tt.valid && (cmd.Name != "Books" || cmd.MetadataLanguage != tt.language) {
				t.Fatal("canonical create normalization/default lost")
			}
		})
	}
	installCases := []struct {
		name, body   string
		organization bool
		valid        bool
	}{
		{"opaque_ids", `{"artifact_key":"fixture","provider_source_id":" a/../b ","root_entry_id":" literal ","enabled":false,"config":{}}`, false, true},
		{"organization_target_presence", `{"artifact_key":"fixture","provider_source_id":"collection","root_entry_id":"root","enabled":true,"config":{},"organization_id":null}`, true, false},
		{"null_enabled", `{"artifact_key":"fixture","provider_source_id":"collection","root_entry_id":"root","enabled":null,"config":{}}`, false, false},
		{"missing_config", `{"artifact_key":"fixture","provider_source_id":"collection","root_entry_id":"root","enabled":false}`, false, false},
		{"empty_config_value", `{"artifact_key":"fixture","provider_source_id":"collection","root_entry_id":"root","enabled":false,"config":{"storage":null}}`, false, false},
		{"revision_without_key", `{"artifact_key":"fixture","provider_source_id":"collection","root_entry_id":"root","enabled":false,"config":{},"expected_revision":1}`, false, false},
		{"key_without_revision", `{"artifact_key":"fixture","provider_source_id":"collection","root_entry_id":"root","enabled":false,"config":{},"source_key":"00000000-0000-0000-0000-000000000001"}`, false, false},
	}
	for _, tt := range installCases {
		t.Run(tt.name, func(t *testing.T) {
			var cmd nativestorage.InstallCommand
			fields, err := nativeStorageDecodeJSON([]byte(tt.body), &cmd)
			scope := auth.AdminScopePlatform
			if tt.organization {
				scope = auth.AdminScopeOrganization
			}
			if err == nil {
				err = nativeStorageInstallRequest(&cmd, fields, scope)
			}
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v want=%v", err == nil, tt.valid)
			}
			if tt.name == "opaque_ids" && (cmd.ProviderSourceID != " a/../b " || cmd.RootEntryID != " literal ") {
				t.Fatal("opaque IDs were normalized")
			}
		})
	}
}

func TestNativeOnboardingHTTPPaginationAndIDBounds(t *testing.T) {
	for _, query := range []string{"limit=0", "limit=101", "limit=1&limit=2", "after=a&after=b", "unknown=1", "limit=1.5", "limit=2147483648", "after=", "limit=%zz"} {
		r := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		if _, _, err := nativeStoragePage(r); err == nil {
			t.Fatalf("invalid pagination accepted: %s", query)
		}
	}
	for _, query := range []string{"", "limit=1", "limit=100&after=7"} {
		r := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		if _, limit, err := nativeStoragePage(r); err != nil || limit <= 0 || limit > 100 {
			t.Fatal("valid pagination rejected")
		}
	}
	for _, id := range []string{"0", "-1", "+1", "1.0", "2147483648", "18446744073709551615", " 1", ""} {
		if _, err := nativeStoragePositiveID(id); err == nil {
			t.Fatalf("invalid PostgreSQL integer ID accepted: %s", id)
		}
	}
	if id, err := nativeStoragePositiveID("2147483647"); err != nil || id != 2147483647 {
		t.Fatal("valid PostgreSQL integer ID rejected")
	}
	for _, key := range []string{"invalid", "00000000-0000-0000-0000-000000000000"} {
		if _, err := nativeStorageUUID(key); err == nil {
			t.Fatal("invalid/nil UUID accepted")
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/", strings.NewReader("body"))
	if err := nativeStorageNoBody(r); err == nil {
		t.Fatal("GET body accepted")
	}
}

func TestNativeOnboardingHTTPMissingDependenciesAndHiddenSerialization(t *testing.T) {
	h := NewBloemNativeStorageManagementHandler(nil, nil)
	for _, call := range []func(http.ResponseWriter) bool{h.sourceAvailable, h.librariesAvailable} {
		w := httptest.NewRecorder()
		if call(w) {
			t.Fatal("missing dependency advertised available")
		}
		assertNativeError(t, w, 503, "native_storage_unavailable")
	}
	capabilities := nativeStorageCapabilityProjection()
	for key, value := range capabilities {
		if value == true {
			t.Fatalf("unproved capability advertised: %s", key)
		}
	}
	w := httptest.NewRecorder()
	status := nativestorage.LibraryStatus{LibraryID: 7, CreationKey: uuid.New(), LibraryRevision: 3, State: "source_unavailable"}
	nativeStorageWrite(w, 200, struct {
		Library nativestorage.LibraryStatus `json:"library"`
	}{status})
	var body struct {
		Library map[string]any `json:"library"`
	}
	if json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatal("library wire invalid")
	}
	for _, field := range []string{"source_key", "source_revision", "binding_id"} {
		value, present := body.Library[field]
		if !present || value != nil {
			t.Fatalf("hidden %s must be explicit null", field)
		}
	}
	for _, forbidden := range []string{"config", "install_path", "checksum", "latest_installation_id", "owner_id"} {
		if _, present := body.Library[forbidden]; present {
			t.Fatalf("private %s exposed", forbidden)
		}
	}
}

func TestNativeOnboardingHTTPUnknownOperationTextIsNotDisclosed(t *testing.T) {
	err := &catalog.MutationOutcomeUnknown{OperationID: uuid.New(), Operation: "private-secret", Cause: errors.New("private-secret")}
	w := httptest.NewRecorder()
	writeNativeStorageError(w, err, false, nil)
	assertNativeError(t, w, 503, "mutation_outcome_unknown")
	if strings.Contains(w.Body.String(), "private-secret") {
		t.Fatal("unapproved operation text disclosed")
	}
}
