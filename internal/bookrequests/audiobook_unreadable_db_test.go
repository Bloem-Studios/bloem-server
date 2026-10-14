package bookrequests

import (
	"context"
	"sync/atomic"
	"testing"
)

// A download client the backend cannot read keeps the request's state and
// counters: an outage never fails a download as lost, and it does not wipe
// the misses counted before it. An outage longer than an hour shows in the
// admin stuck view.
func TestAudiobookUnreadableClientWaitsDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	var answer atomic.Value
	answer.Store("downloading")
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		switch answer.Load().(string) {
		case "unreadable":
			return checkOutcome(AudiobookProviderUnreadable, "unknown", nil), nil
		case "missing":
			return checkOutcome("unknown", "missing", nil), nil
		}
		return checkOutcome("downloading", "downloading", nil), nil
	}
	pass := func() {
		t.Helper()
		env.due(t, r.ID)
		env.reconcile(t)
	}
	stuck := func() string {
		t.Helper()
		a, err := env.s.AudiobookAcquisition(t.Context(), env.admin, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		return a.StuckReason
	}
	env.reconcile(t)
	pass()
	answer.Store("unreadable")
	for i := 1; i <= audiobookMaxFailures+1; i++ {
		pass()
		row, got := env.row(t, r.ID), env.get(t, r.ID)
		if row.providerStatus != "downloading" || row.failures != 0 || got.Fulfilment != "downloading" || got.Status != "approved" {
			t.Fatalf("unreadable check %d changed the request: %+v %+v", i, row, got)
		}
	}
	if a, err := env.s.AudiobookAcquisition(t.Context(), env.admin, r.ID); err != nil || a.ExternalStatus != "unknown" || a.Stuck {
		t.Fatalf("a short outage: %+v %v", a, err)
	}
	// Repeated unreadable checks keep the time the outage began.
	bookSQL(t, env.pool, `UPDATE bloem_audiobook_request_acquisitions SET updated_at=now()-interval '2 hours' WHERE request_id=$1`, r.ID)
	pass()
	if reason := stuck(); reason != "unreachable" {
		t.Fatalf("a two-hour outage is not surfaced: %q", reason)
	}
	list, err := env.s.ListAudiobookAcquisitions(t.Context(), env.admin, "stuck", "", 10)
	if err != nil || len(list.Acquisitions) != 1 || list.Acquisitions[0].StuckReason != "unreachable" {
		t.Fatalf("stuck list: %+v %v", list, err)
	}
	answer.Store("downloading")
	pass()
	if reason := stuck(); reason != "" {
		t.Fatalf("a readable client is still stuck: %q", reason)
	}

	// Misses counted before an outage stay counted, and the outage adds none.
	answer.Store("missing")
	pass()
	answer.Store("unreadable")
	pass()
	pass()
	if row := env.row(t, r.ID); row.providerStatus != "unknown" || row.failures != 1 || env.get(t, r.ID).Fulfilment != "retrying" {
		t.Fatalf("outage after a miss: %+v", row)
	}
	answer.Store("missing")
	for range audiobookMaxFailures - 1 {
		pass()
	}
	if got := env.get(t, r.ID); got.Fulfilment != "failed" || got.OutcomeReason != audiobookReasonLost {
		t.Fatalf("confirmed misses no longer end as lost: %+v", got)
	}
	if attention := env.notices.attentions(); len(attention) != 1 {
		t.Fatalf("attention notices: %v", attention)
	}
}
