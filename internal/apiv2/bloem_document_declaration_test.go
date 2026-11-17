package apiv2

import (
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// TestDocumentDeclarationKeepsDeclaredErrors: a status the operation declares
// itself (updateProfile's 409) survives the class table being merged in, and
// the shared statuses join it.
func TestDocumentDeclarationKeepsDeclaredErrors(t *testing.T) {
	op := &Operation{Operation: huma.Operation{Method: http.MethodPatch, Path: Prefix + "/x/{id}", OperationID: "patchX", Errors: []int{http.StatusConflict}}, Class: ClassProfileScoped, RetrySafety: RetrySafetyNonRetryable}
	documentDeclaration(op, reflect.TypeOf(ProfileUpdateInput{}))
	want := map[int]bool{http.StatusConflict: true, http.StatusRequestTimeout: true, http.StatusNotFound: true, http.StatusUnsupportedMediaType: true}
	for _, s := range op.Errors {
		delete(want, s)
	}
	if len(want) != 0 {
		t.Fatalf("errors %v lack %v", op.Errors, want)
	}
}

// TestDeclaredResponseDescriptionSurvivesRegistration: a description a
// registration declares on a status it also lists in Errors is what the
// document carries, not the bare status text Huma's defineErrors writes.
func TestDeclaredResponseDescriptionSurvivesRegistration(t *testing.T) {
	doc := generatedDocument(t)
	op := doc["paths"].(map[string]any)["/api/v2/settings/values/nav.shortcuts/item"].(map[string]any)["put"].(map[string]any)
	resp := op["responses"].(map[string]any)[strconv.Itoa(http.StatusConflict)].(map[string]any)
	if got := resp["description"]; got != navigationShortcutConflictDescription {
		t.Fatalf("409 description = %q", got)
	}
	if _, ok := resp["content"].(map[string]any)[problemContentType]; !ok {
		t.Fatalf("409 lost its problem content: %v", resp)
	}
}
