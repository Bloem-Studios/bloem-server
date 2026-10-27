package apiv2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// The native document restates two shapes that live unexported in the handlers
// package, because the document is generated from types in this package and the
// handler owns the bytes on the wire. Two declarations of one shape drift the
// moment someone adds a field to only one of them, and the failure is invisible:
// the document simply stops describing what the server sends.
//
// These tests compare the JSON field sets of the two declarations. They use the
// handler's own exported probe functions where available and reflection over the
// served response otherwise, so they break when the wire shape changes rather
// than when a comment does.

func jsonFieldNames(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling %T: %v", v, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshalling %T: %v", v, err)
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// The identity probe is public and a client reads it before holding any
// credential, so a field the document omits is one a client will not know to
// parse.
func TestBloemServerIdentityDocumentMatchesTheServedShape(t *testing.T) {
	t.Parallel()

	documented := jsonFieldNames(t, BloemServerIdentity{})
	served := jsonFieldNames(t, handlers.BloemServerIdentityWireShape())

	if !reflect.DeepEqual(documented, served) {
		t.Errorf("the native document and the served identity response disagree.\n"+
			"documented: %v\nserved:     %v\n"+
			"Add the field to BloemServerIdentity in bloem_native_identity.go, or remove it from both.",
			documented, served)
	}
}

// Organization membership carries two revisions a client caches against.
// Dropping one from the document means a client never learns to watch it.
func TestBloemOrganizationDocumentMatchesTheServedShape(t *testing.T) {
	t.Parallel()

	documented := jsonFieldNames(t, BloemOrganization{})
	served := jsonFieldNames(t, handlers.BloemOrganizationWireShape())

	if !reflect.DeepEqual(documented, served) {
		t.Errorf("the native document and the served organization entry disagree.\n"+
			"documented: %v\nserved:     %v\n"+
			"Add the field to BloemOrganization in bloem_native_identity.go, or remove it from both.",
			documented, served)
	}
}

// An offline queue replays positions hours later, so updated_at is what keeps a
// stale replay from winning a last-write-wins comparison. A document that omits
// it produces clients that never send it, and those clients silently overwrite
// newer server state.
func TestBloemSyncProgressDocumentMatchesTheServedShape(t *testing.T) {
	t.Parallel()

	documented := jsonFieldNames(t, BloemSyncProgressItem{})
	served := jsonFieldNames(t, handlers.BloemSyncProgressItemWireShape())
	if !reflect.DeepEqual(documented, served) {
		t.Errorf("submitted progress item disagrees.\ndocumented: %v\nserved:     %v", documented, served)
	}
}

// The per-item result is what lets a client make progress past one bad entry
// instead of retrying a whole batch forever.
func TestBloemSyncProgressResultDocumentMatchesTheServedShape(t *testing.T) {
	t.Parallel()

	documented := jsonFieldNames(t, BloemSyncProgressResult{})
	served := jsonFieldNames(t, handlers.BloemSyncProgressResultWireShape())
	if !reflect.DeepEqual(documented, served) {
		t.Errorf("progress result disagrees.\ndocumented: %v\nserved:     %v", documented, served)
	}
}

// Person detail is restated because its response lives unexported in handlers.
// The filmography entry matters most: Kind is the item's kind, not the credit's,
// and a client that confuses them renders the wrong destination.
func TestBloemPersonDetailDocumentMatchesTheServedShape(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name       string
		documented any
		served     any
	}{
		{"person", BloemPersonDetail{}, handlers.BloemPersonDetailWireShape()},
		{"filmography entry", BloemPersonFilmographyEntry{}, handlers.BloemPersonFilmographyWireShape()},
	} {
		documented := jsonFieldNames(t, c.documented)
		served := jsonFieldNames(t, c.served)
		if !reflect.DeepEqual(documented, served) {
			t.Errorf("%s disagrees.\ndocumented: %v\nserved:     %v", c.name, documented, served)
		}
	}
}

// The item-collections document is restated because the handler owns it
// unexported.
func TestBloemItemCollectionsDocumentMatchesTheServedShape(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name       string
		documented any
		served     any
	}{
		{"document", BloemItemCollections{}, handlers.BloemItemCollectionsWireShape()},
		{"collection entry", BloemItemCollection{}, handlers.BloemItemCollectionWireShape()},
	} {
		documented := jsonFieldNames(t, c.documented)
		served := jsonFieldNames(t, c.served)
		if !reflect.DeepEqual(documented, served) {
			t.Errorf("%s disagrees.\ndocumented: %v\nserved:     %v", c.name, documented, served)
		}
	}
}

// The inbox envelopes are restated because the handler owns them unexported.
// The rows inside are not restated -- both sides name
// notifications.DeliveryRowPayload -- so only the envelopes can drift, and
// this is what stops them.
func TestBloemNotificationDocumentMatchesTheServedShape(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name       string
		documented any
		served     any
	}{
		{"inbox page", BloemNotificationPage{}, handlers.BloemNotificationPageWireShape()},
		{"sync page", BloemNotificationSyncPage{}, handlers.BloemNotificationSyncPageWireShape()},
		{"page block", BloemNotificationPageInfo{}, handlers.BloemNotificationPageInfoWireShape()},
	} {
		documented := jsonFieldNames(t, c.documented)
		served := jsonFieldNames(t, c.served)
		if !reflect.DeepEqual(documented, served) {
			t.Errorf("%s disagrees.\ndocumented: %v\nserved:     %v\n"+
				"Add the field to bloem_native_notifications.go, or remove it from both.",
				c.name, documented, served)
		}
	}
}

// servedJSONFieldNames returns the top-level member names of a served JSON
// object, sorted.
func servedJSONFieldNames(t *testing.T, raw []byte) []string {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("served body is not a JSON object: %v: %s", err, raw)
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type bloemNoMemberships struct{}

func (bloemNoMemberships) ListMemberships(context.Context, int) ([]tenancy.Membership, error) {
	return nil, nil
}

func (bloemNoMemberships) GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error) {
	return tenancy.Organization{}, tenancy.ErrOrganizationNotFound
}

// The organization listing is an object wrapping its entries. The document
// described a bare array for as long as only the entries were compared.
func TestBloemOrganizationListDocumentMatchesTheServedEnvelope(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, BloemPrefix+"/organizations", nil)
	request = request.WithContext(middleware.SetClaims(request.Context(), &auth.Claims{UserID: 1}))
	response := httptest.NewRecorder()
	handlers.NewBloemSystemHandler(bloemNoMemberships{}).HandleOrganizations(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	served := servedJSONFieldNames(t, response.Body.Bytes())
	documented := jsonFieldNames(t, BloemOrganizationsOutput{}.Body)
	if !reflect.DeepEqual(documented, served) {
		t.Errorf("organization list envelope disagrees.\ndocumented: %v\nserved:     %v", documented, served)
	}
	var body struct {
		Organizations json.RawMessage `json:"organizations"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || string(body.Organizations) != "[]" {
		t.Errorf("an account without memberships must list [], got %s", body.Organizations)
	}
}

// The capability probe is restated here because the handler owns its shape
// unexported. Both levels are compared: the document and the features object.
func TestBloemCapabilityDocumentMatchesTheServedShape(t *testing.T) {
	t.Parallel()

	response := httptest.NewRecorder()
	handlers.NewBloemSystemHandler(nil).HandleCapabilities(response, httptest.NewRequest(http.MethodGet, BloemPrefix+"/capabilities", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if served, documented := servedJSONFieldNames(t, response.Body.Bytes()), jsonFieldNames(t, BloemCapabilities{}); !reflect.DeepEqual(documented, served) {
		t.Errorf("capability document disagrees.\ndocumented: %v\nserved:     %v", documented, served)
	}
	var body struct {
		Features json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if served, documented := servedJSONFieldNames(t, body.Features), jsonFieldNames(t, BloemCapabilityFeatures{}); !reflect.DeepEqual(documented, served) {
		t.Errorf("capability features disagree.\ndocumented: %v\nserved:     %v", documented, served)
	}
}

// The public probes run no gate, so the only statuses are the ones their chi
// handlers write: identity answers 503 while it cannot resolve, and the
// capability probe never fails. Neither may inherit the v2 gate class's
// Problem set or an authentication requirement.
func TestBloemPublicProbesDocumentOnlyTheirServedStatuses(t *testing.T) {
	t.Parallel()

	response := httptest.NewRecorder()
	handlers.NewServerIdentityHandler(nil, nil, nil).HandleGetServerIdentity(response, httptest.NewRequest(http.MethodGet, BloemPrefix+"/server/identity", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unresolvable identity status = %d, want 503", response.Code)
	}

	doc := bloemWebDocument(t)
	for path, want := range map[string][]string{
		"/server/identity": {"200", "503"},
		"/capabilities":    {"200"},
	} {
		op := bloemDocObject(t, doc, "paths", BloemPrefix+path, "get")
		var got []string
		for status := range bloemDocObject(t, op, "responses") {
			got = append(got, status)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s responses = %v, want %v", path, got, want)
		}
		if security, ok := op["security"].([]any); !ok || len(security) != 0 {
			t.Errorf("%s must be declared public, security = %v", path, op["security"])
		}
	}
	unavailable := bloemDocObject(t, doc, "paths", BloemPrefix+"/server/identity", "get", "responses", "503", "content", "application/json", "schema")
	bloemDocFields(t, bloemDocSchema(t, doc, unavailable), "error", "code", "message")
}
