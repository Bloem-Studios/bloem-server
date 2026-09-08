package pgstore

import (
	"testing"
)

func TestAutomaticFirstAdmissionRetainsIntentAndReceipt(t *testing.T) {
	f, p, _ := admissionFixture(t)
	intent, err := p.retainAutomaticFirstAdmission(t.Context(), f.store.userID)
	if err != nil {
		t.Fatal(err)
	}
	// Recover after a process stops between intent retention and application.
	first, err := p.AutomaticFirstAdmission(t.Context(), f.store.userID)
	if err != nil || first.Intent != intent || first.State != "admitted" {
		t.Fatalf("apply: %+v %v", first, err)
	}
	// Discarding the first response simulates uncertainty after a committed apply.
	replay, err := p.AutomaticFirstAdmission(t.Context(), f.store.userID)
	if err != nil || replay.Intent != intent || replay.State != "already_admitted" || !replay.AdmittedAt.Equal(first.AdmittedAt) {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	var receipts int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM playback_first_admissions WHERE user_id=$1 AND intent_id=$2 AND source_id=$3`, intent.AccountID, intent.IntentID, intent.SourceID).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("receipt: %d %v", receipts, err)
	}
}

func TestAutomaticFirstAdmissionConcurrentNodes(t *testing.T) {
	f, p, _ := admissionFixture(t)
	type result struct {
		decision FirstAdmissionDecision
		err      error
	}
	results := make(chan result, 8)
	for range 8 {
		go func() { d, err := p.AutomaticFirstAdmission(t.Context(), f.store.userID); results <- result{d, err} }()
	}
	var intent FirstAdmissionIntent
	for i := range 8 {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		if i == 0 {
			intent = got.decision.Intent
		}
		if got.decision.Intent != intent {
			t.Fatal("concurrent node selected another intent")
		}
	}
}

func TestAutomaticFirstAdmissionRefusesExistingAuthority(t *testing.T) {
	for _, kind := range []string{"marker", "admitting", "blocked", "retiring", "sink", "backend", "identity"} {
		t.Run(kind, func(t *testing.T) {
			f, p, intent := admissionFixture(t)
			var err error
			switch kind {
			case "marker":
				_, err = f.pool.Exec(t.Context(), `INSERT INTO playback_source_markers(user_id,source_id,selection_generation,gate) VALUES($1,$2,1,'writable')`, intent.AccountID, intent.SourceID)
			case "admitting", "blocked", "retiring":
				_, err = f.pool.Exec(t.Context(), `INSERT INTO playback_source_registrations(user_id,backend,source_id,selection_generation,admission_id,admission_state) VALUES($1,'postgres',$2,1,$3,$4)`, intent.AccountID, intent.SourceID, intent.IntentID, kind)
			case "sink":
				f.install(t)
			case "backend":
				_, err = f.pool.Exec(t.Context(), `UPDATE server_settings SET value='sqlite' WHERE key='userdb.backend'`)
			case "identity":
				if _, err = p.retainAutomaticFirstAdmission(t.Context(), intent.AccountID); err != nil {
					t.Fatal(err)
				}
				_, err = f.pool.Exec(t.Context(), `UPDATE users SET username=username||'-renamed' WHERE id=$1`, intent.AccountID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.AutomaticFirstAdmission(t.Context(), intent.AccountID); err == nil {
				t.Fatal("conflicting state admitted")
			}
			var receipts int
			if err = f.pool.QueryRow(t.Context(), `SELECT count(*) FROM playback_first_admissions WHERE user_id=$1`, intent.AccountID).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatalf("unexpected receipt: %d %v", receipts, err)
			}
		})
	}
}
