package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/google/uuid"
)

// A field added, removed, or made non-optional in either the producer or DTO
// breaks this check. Exact JSON values preserve null versus absent semantics.
func nativeStorageContractRoundTrip(t *testing.T, raw []byte, target any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("wire shape changed: %s => %s", raw, encoded)
	}
}

func TestNativeStorageContractCapabilityProjection(t *testing.T) {
	h := &BloemNativeStorageManagementHandler{}
	raw, err := json.Marshal(h.nativeStorageCapabilities(t.Context()))
	if err != nil {
		t.Fatal(err)
	}
	nativeStorageContractRoundTrip(t, raw, &nativeStorageCapabilitiesResponse{})
}

func TestNativeStorageContractErrorProjection(t *testing.T) {
	key := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	revision := int64(7)
	cases := []struct {
		name       string
		err        error
		sourceOnly bool
	}{
		{"unavailable", errors.New("private detail"), false},
		{"source_conflict", &catalog.NativeOnboardingError{Code: "revision_conflict", CurrentSourceRevision: &revision}, true},
		{"reconcile", &catalog.MutationOutcomeUnknown{OperationID: key, Operation: "install", SourceKey: &key}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeNativeStorageError(w, tt.err, tt.sourceOnly)
			nativeStorageContractRoundTrip(t, w.Body.Bytes(), &nativeStorageErrorResponse{})
		})
	}
}

func TestNativeStorageContractNullableAndBoundedResponses(t *testing.T) {
	raw, err := json.Marshal(nativeStorageSourceResponse{})
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]map[string]any
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"organization_id", "installation_id"} {
		if value, ok := source["source"][key]; !ok || value != nil {
			t.Fatalf("%s must be explicit null", key)
		}
	}
}

func TestNativeStorageContractMinimalOrganizationInstall(t *testing.T) {
	raw, err := json.Marshal(nativeStorageOrganizationInstallRequest{
		ArtifactKey: "fixture", ProviderSourceID: "fixture-source", RootEntryID: "fixture-root",
		Enabled: false, Config: map[string]map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var install nativestorage.InstallCommand
	fields, err := nativeStorageDecodeJSON(raw, &install)
	if err != nil {
		t.Fatal(err)
	}
	if err := nativeStorageInstallRequest(&install, fields, auth.AdminScopeOrganization); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"organization_id", "source_key", "expected_revision"} {
		if _, present := fields[name]; present {
			t.Fatalf("%s must remain omitted", name)
		}
	}
}
