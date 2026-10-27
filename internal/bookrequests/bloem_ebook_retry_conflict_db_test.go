package bookrequests

import (
	"context"
	"errors"
	"testing"
)

// A retry whose linked library is no longer eligible finds the request changed
// under it: a conflict to reload, not a bad request.
func TestEbookRetryAfterLibraryBecameIneligibleConflictsDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	integration, folder, _ := ebookIntegrationFixture(t, pool)
	admin := v
	admin.Admin = true
	if _, err := s.SetEbookIntegrationLibrary(t.Context(), admin, integration, folder); err != nil {
		t.Fatal(err)
	}
	r := approveEbook(t, s, v, "9780306406157")
	s.SetEbookFulfillment(ebookRouterFixture{fulfill: func(context.Context, DispatchInput) (Receipt, error) {
		return Receipt{ExternalID: "retained-unknown", Status: "unknown"}, nil
	}, status: func(context.Context, DispatchInput, Receipt) (ProviderStatus, error) {
		return ProviderStatus{Status: "unknown"}, nil
	}})
	for range 8 {
		if _, err := s.ReconcileEbooks(t.Context(), 100); err != nil {
			t.Fatal(err)
		}
		dueEbook(t, pool, r.ID)
	}
	failed, err := s.GetEbook(t.Context(), v, r.ID)
	if err != nil || failed.Fulfilment != "failed" {
		t.Fatalf("setup: %+v %v", failed, err)
	}
	bookSQL(t, pool, `UPDATE media_folders SET enabled=false WHERE id=$1`, folder)

	_, err = s.ActEbook(t.Context(), admin, r.ID, "retry", ActionInput{ExpectedRevision: failed.Revision})
	if !errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid) {
		t.Fatalf("retry with an ineligible library = %v; want only ErrConflict", err)
	}
	if got, err := s.GetEbook(t.Context(), v, r.ID); err != nil || got.Fulfilment != "failed" || got.Revision != failed.Revision {
		t.Fatalf("refused retry changed the request: %+v %v", got, err)
	}
}
