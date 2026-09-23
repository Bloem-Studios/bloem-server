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
