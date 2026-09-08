package pgstore

import (
	"github.com/Silo-Server/silo-server/internal/userstore"
	"testing"
)

func TestDiscardUnreceiptedAdmissionRequiresExactUnusedBinding(t *testing.T) {
	for _, kind := range []string{"unused", "wrong_id", "blocked", "receipt", "sink"} {
		t.Run(kind, func(t *testing.T) {
			f, p, intent := admissionFixture(t)
			if kind == "receipt" {
				if _, err := p.FirstAdmission(t.Context(), intent, true); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.pool.Exec(t.Context(), `INSERT INTO playback_source_markers(user_id,source_id,selection_generation,gate) VALUES($1,$2,1,'writable')`, intent.AccountID, intent.SourceID); err != nil {
					t.Fatal(err)
				}
				if _, err := f.pool.Exec(t.Context(), `INSERT INTO playback_source_registrations(user_id,backend,source_id,selection_generation,admission_id,admission_state) VALUES($1,'postgres',$2,1,$3,'admitting')`, intent.AccountID, intent.SourceID, intent.IntentID); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "wrong_id":
				intent.IntentID = "11111111-1111-4111-8111-111111111111"
			case "blocked":
				if _, err := f.pool.Exec(t.Context(), `UPDATE playback_source_registrations SET admission_state='blocked' WHERE user_id=$1`, intent.AccountID); err != nil {
					t.Fatal(err)
				}
			case "sink":
				handle, err := p.OpenPlaybackSink(t.Context(), userstore.PlaybackSourceRef{Backend: "postgres", AccountID: intent.AccountID, SourceID: intent.SourceID, SelectionGeneration: 1})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = handle.InstallPlaybackAuthority(t.Context(), userstore.InstallPlaybackAuthorityRequest{Scope: f.scope, Next: f.fence}); err != nil {
					t.Fatal(err)
				}
			}
			planned, err := p.DiscardUnreceiptedAdmission(t.Context(), intent, false)
			if kind != "unused" {
				if err == nil {
					t.Fatal("unsafe cleanup accepted")
				}
				return
			}
			if err != nil || planned.State != "discardable" {
				t.Fatalf("plan: %+v %v", planned, err)
			}
			applied, err := p.DiscardUnreceiptedAdmission(t.Context(), intent, true)
			if err != nil || applied.State != "discarded" {
				t.Fatalf("apply: %+v %v", applied, err)
			}
			replay, err := p.DiscardUnreceiptedAdmission(t.Context(), intent, true)
			if err != nil || replay.State != "absent" {
				t.Fatalf("replay: %+v %v", replay, err)
			}
			if _, err := p.AutomaticFirstAdmission(t.Context(), intent.AccountID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
