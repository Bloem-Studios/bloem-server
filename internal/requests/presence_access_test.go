package requests

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

type presenceScopeFunc func(context.Context, access.ResolveInput) (access.Scope, error)

func (f presenceScopeFunc) Resolve(ctx context.Context, in access.ResolveInput) (access.Scope, error) {
	return f(ctx, in)
}

type presenceItemsFunc func(context.Context, []string, catalog.AccessFilter) (map[string]bool, error)

func (f presenceItemsFunc) EnsureAccessibleIDs(ctx context.Context, ids []string, filter catalog.AccessFilter) (map[string]bool, error) {
	return f(ctx, ids, filter)
}

func TestRequestPresenceUsesCurrentRequesterScope(t *testing.T) {
	store := newFakeStore()
	store.settings.RequestsEnabled = true
	req := completedRequestFixture("request", 42)
	store.requests[req.ID] = req
	store.unnotified = []string{req.ID}
	service := NewService(store, &fakeTMDBClient{}, &fakePresence{available: map[MediaType]map[int]bool{MediaTypeMovie: {42: true}}})
	notifier := &fakeNotifier{}
	service.SetFulfillmentNotifier(notifier)
	allowed := false
	var scopeErr error
	var itemErr error
	service.SetPresenceAccess(presenceScopeFunc(func(_ context.Context, in access.ResolveInput) (access.Scope, error) {
		if in.UserID != 7 || in.ProfileID != "profile-1" || !in.SkipPINVerification {
			t.Fatalf("wrong requester: %+v", in)
		}
		return access.Scope{UserID: 7, ProfileID: "profile-1", AllowedLibraryIDs: []int{11}, DisabledLibraryIDs: []int{12}, MaxContentRating: "PG"}, scopeErr
	}), presenceItemsFunc(func(_ context.Context, ids []string, filter catalog.AccessFilter) (map[string]bool, error) {
		if len(filter.AllowedLibraryIDs) != 1 || filter.AllowedLibraryIDs[0] != 11 {
			t.Fatalf("missing library ceiling: %+v", filter)
		}
		if len(filter.DisabledLibraryIDs) != 1 || filter.DisabledLibraryIDs[0] != 12 || filter.MaxContentRating != "PG" {
			t.Fatalf("lost catalog constraints: %+v", filter)
		}
		return map[string]bool{fakePresenceContentID(MediaTypeMovie, 42): allowed}, itemErr
	}))
	viewer := Viewer{UserID: 7, ProfileID: "profile-1"}
	check := func(want bool) {
		t.Helper()
		available, err := service.requestAvailable(t.Context(), *req)
		if err != nil || available != want {
			t.Fatalf("availability=%t err=%v want=%t", available, err, want)
		}
		detail, err := service.GetRequest(t.Context(), viewer, req.ID)
		if err != nil {
			t.Fatal(err)
		}
		if (detail.LibraryContentID != "") != want {
			t.Fatalf("library link=%q want available=%t", detail.LibraryContentID, want)
		}
	}
	check(false)
	service.notifyFulfilledPending(t.Context())
	if len(notifier.requestIDs) != 0 || len(store.notified) != 0 {
		t.Fatal("private copy triggered fulfillment notification")
	}
	allowed = true
	check(true)
	// Reusing the same service/request must clear an earlier library link.
	allowed = false
	check(false)
	scopeErr = errors.New("organization unavailable")
	if _, err := service.requestAvailable(t.Context(), *req); err == nil {
		t.Fatal("scope error treated as global presence")
	}
	service.notifyFulfilledPending(t.Context())
	if len(notifier.requestIDs) != 0 || len(store.notified) != 0 {
		t.Fatal("scope error completed notification")
	}
	scopeErr = nil
	itemErr = errors.New("catalog unavailable")
	if _, err := service.requestAvailable(t.Context(), *req); err == nil {
		t.Fatal("catalog error treated as global presence")
	}
	service.notifyFulfilledPending(t.Context())
	if len(notifier.requestIDs) != 0 || len(store.notified) != 0 {
		t.Fatal("catalog error completed notification")
	}
	itemErr = nil
	allowed = true
	service.notifyFulfilledPending(t.Context())
	if len(notifier.requestIDs) != 1 || len(store.notified) != 1 {
		t.Fatal("accessible request was not notified")
	}
}
