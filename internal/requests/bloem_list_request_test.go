package requests

import (
	"context"
	"errors"
	"strconv"
	"testing"
)

func TestListRequestNativeReceiptReplayDoesNotDebitQuotaAgain(t *testing.T) {
	store := newFakeStore()
	store.settings.RequestsEnabled = true
	store.settings.GlobalMaxRequests = 1
	s := newTestService(store)
	input := CreateRequestInput{MediaType: MediaTypeMovie, TMDBID: 42, Title: "Source title"}
	key := "synthetic-list-request-key"
	first, err := s.CreateListRequest(t.Context(), testViewer(1), input, key)
	if err != nil {
		t.Fatal(err)
	}
	input.Title = "Changed source display title"
	again, err := s.CreateListRequest(t.Context(), testViewer(1), input, key)
	if err != nil || again.ID != first.ID || len(store.created) != 1 {
		t.Fatalf("replayed request duplicated/debited quota: %v %v count=%d", again, err, len(store.created))
	}
	input.TMDBID = 43
	if _, err = s.CreateListRequest(t.Context(), testViewer(1), input, key); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("changed native identity reused receipt: %v", err)
	}
}

func TestListRequestWitnessDoesNotChangeOrdinaryNativeCreation(t *testing.T) {
	store := newFakeStore()
	store.settings.RequestsEnabled = true
	s := newTestService(store)
	ctx := t.Context()
	if _, err := s.CreateListRequest(ctx, testViewer(1), CreateRequestInput{MediaType: MediaTypeMovie, TMDBID: 42, Title: "Linked"}, "synthetic-source-id-witness-key"); err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRequest(ctx, testViewer(1), CreateRequestInput{MediaType: MediaTypeMovie, TMDBID: 43, Title: "Ordinary"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = strconv.ParseUint(r.ID, 10, 64); err != nil {
		t.Fatal("owned witness escaped into ordinary native creation", r.ID)
	}
}

type listLostReceiptStore struct{ Store }

func (s listLostReceiptStore) CreateRequest(ctx context.Context, in CreateRequestRecord) (*Request, error) {
	if _, err := s.Store.CreateRequest(ctx, in); err != nil {
		return nil, err
	}
	return nil, errors.New("synthetic lost commit response")
}

func TestListRequestRecoversDurableNativeCreationAfterLostReceipt(t *testing.T) {
	store := newFakeStore()
	store.settings.RequestsEnabled = true
	base := newTestService(store)
	base.store = listLostReceiptStore{Store: store}
	input := CreateRequestInput{MediaType: MediaTypeMovie, TMDBID: 42, Title: "Synthetic"}
	r, err := base.CreateListRequest(t.Context(), testViewer(1), input, "synthetic-receipt-recovery-key")
	if err != nil || r == nil || len(store.created) != 1 {
		t.Fatalf("lost receipt did not recover creation: %v %v", r, err)
	}
	_, err = base.CreateListRequest(t.Context(), testViewer(1), input, "synthetic-receipt-recovery-key")
	if err != nil || len(store.created) != 1 {
		t.Fatalf("retry created second request: %v count=%d", err, len(store.created))
	}
	if _, err = base.CreateListRequest(t.Context(), testViewer(2), input, "synthetic-receipt-recovery-key"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("replay crossed owner: %v", err)
	}
}
