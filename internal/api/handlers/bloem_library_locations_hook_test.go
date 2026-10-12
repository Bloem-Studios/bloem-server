package handlers

import (
	"errors"
	"net/http"
	"testing"

	"reflect"
)

func TestLibraryCreationAttachmentHookExists(t *testing.T) {
	// The extension must use the ordinary creation body, never another creator.
	if _, ok := reflect.TypeFor[*LibraryHandler]().MethodByName("CreateLibraryWithAttachment"); !ok {
		t.Fatal("ordinary library creation has no neutral attachment hook")
	}
}

func TestLibraryCreationAttachmentKeepsLegacyExclusivity(t *testing.T) {
	h := &LibraryHandler{}
	_, err := h.CreateLibrary(t.Context(), LibraryCreateRequest{Name: "legacy", Type: "ebooks"})
	var decision *APIError
	if !errors.As(err, &decision) || decision.Status != http.StatusBadRequest {
		t.Fatalf("legacy empty paths: %v", err)
	}
}
