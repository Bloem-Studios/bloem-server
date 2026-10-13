package apiv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	contracts "github.com/Silo-Server/silo-server/contracts/api/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

func TestOperationalDiscoveryImageLadderAndOptionalProfile(t *testing.T) {
	handler := NewHandler(parityDeps(false))
	response := do(t, handler, http.MethodGet, Prefix+"/images/capabilities", "", bearer(memberToken))
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	assertDiscoverySchema(t, response, "ImageCapabilities")
	var actual ImageCapabilities
	if err := json.Unmarshal(response.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	legacy := httptest.NewRecorder()
	handlers.HandleImagesCapability(legacy, httptest.NewRequest(http.MethodGet, "/api/v1/images/capability", nil))
	var want handlers.ImagesCapabilityResponse
	if err := json.Unmarshal(legacy.Body.Bytes(), &want); err != nil {
		t.Fatal(err)
	}
	if !actual.EpisodeStillProvenance || actual.SeasonListArtworkParam != want.SeasonListArtworkParam || actual.SeasonListArtworkParam != "include_artwork" || actual.State != StateAvailable || actual.Revision == "" || actual.Param != want.Param || actual.OriginalMaxWidthPx != want.OriginalMaxWidthPx || !reflect.DeepEqual(actual.Sizes, want.Sizes) || len(actual.Widths) != len(want.Widths) {
		t.Fatalf("v2=%+v legacy=%+v", actual, want)
	}
	for key, expected := range want.Widths {
		width, ok := actual.Widths[key]
		if !ok || width.Small != expected.Small || width.Medium != expected.Medium || width.Large != expected.Large {
			t.Fatalf("image width %s: got %+v, want %+v", key, width, expected)
		}
	}
	requireProblem(t, do(t, handler, http.MethodGet, Prefix+"/images/capabilities", "", nil), TypeAuthenticationRequired)
	requireProblem(t, do(t, handler, http.MethodGet, Prefix+"/images/capabilities", "", with(bearer(memberToken), "X-Profile-Id", "p-locked")), TypeProfileVerificationRequired)
	requireProblem(t, do(t, handler, http.MethodGet, Prefix+"/images/capabilities?unknown=value", "", bearer(memberToken)), TypeValidationFailed)
}

func assertDiscoverySchema(t *testing.T, response *httptest.ResponseRecorder, name string) {
	t.Helper()
	var document, body any
	if err := json.Unmarshal(contracts.OpenAPI, &document); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const url = "https://schema.example.invalid/openapi.json"
	if err := compiler.AddResource(url, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(url + "#/components/schemas/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(body); err != nil {
		t.Fatal(err)
	}
}
