package apiv2

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
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
