package apiv2

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// These are the actual production translators, with no replacement writer.
func TestBloemNativeProblemActualTranslators(t *testing.T) {
	translators := map[string]func(error) *Problem{"service": serviceProblem, "collection": collectionProblem, "library": libraryProblem, "catalog-action": catalogActionProblem}
	for _, code := range []string{"native_library_delete_unsupported", "native_repair_unsupported", "native_local_operation_unsupported", "native_storage_unavailable"} {
		status := 409
		if code == "native_storage_unavailable" {
			status = 503
		}
		p := NewProblem(ProblemType{ID: code, Status: status, Title: http.StatusText(status)}, "Fixed refusal")
		for name, translate := range translators {
			t.Run(name+"/"+code, func(t *testing.T) {
				if got := translate(p); got != p {
					t.Fatalf("direct Problem replaced: %+v", got)
				}
			})
			t.Run(name+"/wrapped/"+code, func(t *testing.T) {
				if got := translate(fmt.Errorf("phase: %w", p)); got != p {
					t.Fatalf("wrapped Problem replaced: %+v", got)
				}
			})
		}
	}
	for name, translate := range translators {
		for _, tc := range []struct {
			status int
			code   string
			want   ProblemType
		}{{401, "unauthorized", TypeAuthenticationRequired}, {403, "forbidden", TypePermissionDenied}, {404, "not_found", TypeNotFound}} {
			t.Run(name+"/ordinary/"+tc.code, func(t *testing.T) {
				got := translate(&handlers.APIError{Status: tc.status, Code: tc.code, Message: "Existing failure"})
				if got.Type != tc.want.URI() {
					t.Fatalf("ordinary type %s", got.Type)
				}
			})
		}
	}
	if got := serviceProblem(&handlers.APIError{Status: 400, Code: "bad_request", Message: "No matching library"}); got.Type != TypeMalformedRequest.URI() {
		t.Fatal(got.Type)
	}
}

// Reuses the real apiv2 router/auth fixture; only the scan service outcome is a
// unit double. This proves HTTP translation, not authenticated database policy.
func TestBloemNativeProblemScanHTTPTranslator(t *testing.T) {
	deps := parityDeps(false)
	f := new(scanControlFixture)
	deps.ScanControls = f
	h := NewHandler(deps)
	admin := with(bearer(adminToken), "X-Profile-Id", "p-primary")
	for _, message := range []string{"No library matches that path", "Multiple libraries match that path"} {
		f.failure = &handlers.APIError{Status: 400, Code: "bad_request", Message: message}
		requireProblem(t, do(t, h, "POST", Prefix+"/scan", `{"path":"/synthetic/unmatched"}`, admin), TypeMalformedRequest)
	}
	for _, tc := range []struct {
		status int
		code   string
		want   ProblemType
	}{{401, "unauthorized", TypeAuthenticationRequired}, {403, "forbidden", TypePermissionDenied}} {
		f.failure = &handlers.APIError{Status: tc.status, Code: tc.code, Message: "Existing denial"}
		requireProblem(t, do(t, h, "POST", Prefix+"/scan", `{"path":"/synthetic"}`, admin), tc.want)
	}
	for _, code := range []string{"native_library_delete_unsupported", "native_repair_unsupported", "native_local_operation_unsupported", "native_storage_unavailable"} {
		status := 409
		if code == "native_storage_unavailable" {
			status = 503
		}
		typ := ProblemType{ID: code, Status: status, Title: http.StatusText(status)}
		f.failure = NewProblem(typ, "Fixed refusal")
		requireProblem(t, do(t, h, "POST", Prefix+"/scan", `{"path":"/synthetic"}`, admin), typ)
	}
}
