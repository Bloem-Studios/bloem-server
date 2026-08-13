package adminpeople

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestServiceSelectionTokenIsBoundedOpaqueAndAuthenticated(t *testing.T) {
	service := NewService(nil, "selection-test-secret")
	reference := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	token, err := service.signSelectionReference(reference)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) > 128 || strings.Contains(token, "12") || strings.Contains(token, "41") {
		t.Fatalf("selection token is not bounded and opaque: %q", token)
	}
	parsed, err := service.parseSelectionReference(token)
	if err != nil {
		t.Fatal(err)
	}
	if parsed != reference {
		t.Fatalf("selection reference = %s, want %s", parsed, reference)
	}
	tampered := token[:len(token)-1] + "A"
	if _, err := service.parseSelectionReference(tampered); !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("tampered token error = %v, want ErrInvalidSelection", err)
	}
}

func TestServiceRejectsUnsupportedBulkActionBeforeDatabaseAccess(t *testing.T) {
	service := NewService(nil, "selection-test-secret")
	_, err := service.ExecuteBulk(t.Context(), uuid.New(), 7, BulkAction{Kind: "delete_accounts", SelectionToken: "opaque"})
	if !errors.Is(err, ErrInvalidBulkAction) {
		t.Fatalf("ExecuteBulk() error = %v, want ErrInvalidBulkAction", err)
	}
}
