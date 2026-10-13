//go:build integration

package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/listcollections"
)

type listDeliveryFixture struct {
	receipts map[string]string
	calls    int
	lose     bool
}

func (d *listDeliveryFixture) Lookup(_ context.Context, s listcollections.Subject, e listcollections.Entry) (listcollections.RequestOutcome, error) {
	key, err := listcollections.RequestReplayKey(s, e)
	return listcollections.RequestOutcome{ID: d.receipts[key]}, err
}
func (d *listDeliveryFixture) Submit(_ context.Context, _ listcollections.Subject, _ listcollections.Entry, key string) (listcollections.RequestOutcome, error) {
	d.calls++
	d.receipts[key] = "native-receipt"
	if d.lose {
		return listcollections.RequestOutcome{}, errors.New("synthetic lost receipt")
	}
	return listcollections.RequestOutcome{ID: d.receipts[key]}, nil
}
func seedListDeliveryBinding(t *testing.T, s *listcollections.Service, subject listcollections.Subject, e listcollections.Entry) string {
	t.Helper()
	ctx := t.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	id := uuid.NewString()
	target, _, err := s.Domain.CreateTx(ctx, tx, subject, listcollections.CreateInput{Scope: "personal", Name: "Synthetic requests"}, id)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(subject)
	_, err = tx.Exec(ctx, `INSERT INTO bloem_list_bindings(id,installation_id,user_id,profile_id,subject,preview_id,create_digest,source_id,signing_key,grant_cipher,scope,target_id,status) VALUES($1,$2,$3,$4,$5,$6,$7,'synthetic',$8,'','personal',$9,'ready')`, id, subject.InstallationID, subject.UserID, subject.ProfileID, raw, uuid.NewString(), make([]byte, 32), strings.Repeat("a", 43), target)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(e)
	_, err = tx.Exec(ctx, `INSERT INTO bloem_list_request_intents(binding_id,identity_key,entry,generation)VALUES($1,'identity',$2,1)`, id, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}
func TestListFollowDeliveryRecoversLostReceiptAcrossTwoBindings(t *testing.T) {
	s, subject, domain := listPublicationFixture(t)
	domain.candidates = nil
	d := &listDeliveryFixture{receipts: map[string]string{}, lose: true}
	s.Requests = d
	e := listcollections.Entry{Kind: "movie", Title: "Synthetic", ExternalIDs: map[string]string{"tmdb": "99"}}
	a := seedListDeliveryBinding(t, s, subject, e)
	e.Title = "Changed source label"
	b := seedListDeliveryBinding(t, s, subject, e)
	got, err := s.DeliverRequests(t.Context(), 10)
	if err != nil || got.Retried != 1 || got.Delivered != 1 || d.calls != 1 {
		t.Fatalf("lost receipt/two-binding replay: %+v calls=%d err=%v", got, d.calls, err)
	}
	if _, err = s.Pool.Exec(t.Context(), `UPDATE bloem_list_request_intents SET next_due=clock_timestamp() WHERE binding_id=ANY($1::uuid[])`, []string{a, b}); err != nil {
		t.Fatal(err)
	}
	got, err = s.DeliverRequests(t.Context(), 10)
	if err != nil || got.Delivered != 1 || d.calls != 1 {
		t.Fatalf("retry duplicated native submit: %+v calls=%d err=%v", got, d.calls, err)
	}
	var count int
	if err = s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_list_request_intents WHERE binding_id=ANY($1::uuid[]) AND status='delivered' AND request_id='native-receipt'`, []string{a, b}).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}
func TestListFollowDeliveryDoesNotSendRemovedDetachedOrAvailableTitles(t *testing.T) {
	for _, mode := range []string{"removed", "detached", "available", "denied", "target_deleted"} {
		t.Run(mode, func(t *testing.T) {
			s, subject, domain := listPublicationFixture(t)
			d := &listDeliveryFixture{receipts: map[string]string{}}
			s.Requests = d
			e := listcollections.Entry{Kind: "movie", Title: "Synthetic", ExternalIDs: map[string]string{"tmdb": "42"}}
			id := seedListDeliveryBinding(t, s, subject, e)
			switch mode {
			case "removed":
				_, err := s.Pool.Exec(t.Context(), `UPDATE bloem_list_request_intents SET wanted=false WHERE binding_id=$1`, id)
				if err != nil {
					t.Fatal(err)
				}
			case "detached":
				if err := s.Disconnect(t.Context(), subject, id); err != nil {
					t.Fatal(err)
				}
			case "denied":
				domain.deniedProfile = subject.ProfileID
			case "target_deleted":
				_, err := s.Pool.Exec(t.Context(), `DELETE FROM user_personal_collections WHERE source_config->>'bloem_list_binding'=$1`, id)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.DeliverRequests(t.Context(), 10); err != nil {
				t.Fatal(err)
			}
			if d.calls != 0 {
				t.Fatal("submitted unwanted native request", mode)
			}
		})
	}
}

func TestListFollowDeliverySkipsPublicationLaneWithoutHoldingRows(t *testing.T) {
	s, subject, domain := listPublicationFixture(t)
	domain.candidates = nil
	d := &listDeliveryFixture{receipts: map[string]string{}}
	s.Requests = d
	seedListDeliveryBinding(t, s, subject, listcollections.Entry{Kind: "movie", Title: "Synthetic", ExternalIDs: map[string]string{"tmdb": "99"}})
	tx, err := s.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if err = listcollections.LaneTx(t.Context(), tx, subject.InstallationID); err != nil {
		t.Fatal(err)
	}
	got, err := s.DeliverRequests(t.Context(), 10)
	if err != nil || got.Delivered != 0 || d.calls != 0 {
		t.Fatalf("dispatch bypassed publication lane: %+v %v", got, err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err = s.DeliverRequests(t.Context(), 10)
	if err != nil || got.Delivered != 1 || d.calls != 1 {
		t.Fatalf("lane did not release: %+v %v", got, err)
	}
}
