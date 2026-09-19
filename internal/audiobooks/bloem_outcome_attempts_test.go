package audiobooks

import (
	"testing"
	"time"
)

func TestBloemUnclaimedOutcomeAttemptHistory(t *testing.T) {
	for _, tc := range []struct {
		name              string
		outcomes          []EnrichmentOutcome
		attempts          int
		parked, completed bool
	}{
		{"failure then success", []EnrichmentOutcome{EnrichmentOutcomeSuccess}, 2, false, true},
		{"failure then skipped", []EnrichmentOutcome{EnrichmentOutcomeSkipped}, 2, true, false},
		{"failure then first no-match", []EnrichmentOutcome{EnrichmentOutcomeNoMatch}, 1, true, false},
		{"consecutive no-match", []EnrichmentOutcome{EnrichmentOutcomeNoMatch, EnrichmentOutcomeNoMatch}, 2, true, false},
		{"terminal no-match", []EnrichmentOutcome{EnrichmentOutcomeNoMatch, EnrichmentOutcomeNoMatch, EnrichmentOutcomeNoMatch}, 3, false, true},
		{"success then new no-match", []EnrichmentOutcome{EnrichmentOutcomeSuccess, EnrichmentOutcomeNoMatch}, 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := newClaimTestPool(t)
			store := newEnrichmentStateStore(pool)
			id := seedAudiobook(t, pool, "attempt-history", "", false)
			if err := store.RecordFailure(t.Context(), id, "", EnrichmentErrorTransient, "fixture transient failure"); err != nil {
				t.Fatal(err)
			}
			for _, outcome := range tc.outcomes {
				if err := store.RecordOutcome(t.Context(), id, "", outcome); err != nil {
					t.Fatal(err)
				}
			}
			var attempts int
			var next, done *time.Time
			var errorClass *string
			if err := pool.QueryRow(t.Context(), `SELECT attempts,next_attempt_at,completed_at,last_error_class FROM audiobook_enrichment_state WHERE content_id=$1`, id).Scan(&attempts, &next, &done, &errorClass); err != nil {
				t.Fatal(err)
			}
			if attempts != tc.attempts || (next != nil) != tc.parked || (done != nil) != tc.completed || errorClass != nil {
				t.Fatalf("attempts/parked/completed/error=%d/%t/%t/%v, want %d/%t/%t/nil", attempts, next != nil, done != nil, errorClass, tc.attempts, tc.parked, tc.completed)
			}
			if next != nil && !next.After(time.Now()) {
				t.Fatal("retry was not parked in the future")
			}
		})
	}
}
