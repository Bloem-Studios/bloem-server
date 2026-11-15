package audiobooks

import (
	"testing"
	"time"
)

func TestBloemUnclaimedOutcomeAttemptHistory(t *testing.T) {
	for _, tc := range []struct {
		name                string
		outcomes            []EnrichmentOutcome
		attempts, noMatches int
		parked, completed   bool
	}{
		{"failure then success", []EnrichmentOutcome{EnrichmentOutcomeSuccess}, 2, 0, false, true},
		{"failure then skipped", []EnrichmentOutcome{EnrichmentOutcomeSkipped}, 2, 0, true, false},
		{"failure then first no-match", []EnrichmentOutcome{EnrichmentOutcomeNoMatch}, 2, 1, true, false},
		{"consecutive no-match", []EnrichmentOutcome{EnrichmentOutcomeNoMatch, EnrichmentOutcomeNoMatch}, 3, 2, true, false},
		{"terminal no-match", []EnrichmentOutcome{EnrichmentOutcomeNoMatch, EnrichmentOutcomeNoMatch, EnrichmentOutcomeNoMatch}, 4, 3, false, true},
		{"success then new no-match", []EnrichmentOutcome{EnrichmentOutcomeSuccess, EnrichmentOutcomeNoMatch}, 3, 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := newClaimTestPool(t)
			store := newEnrichmentStateStore(pool)
			id := seedAudiobook(t, pool, "attempt-history", "", false)
			if err := store.RecordFailure(t.Context(), id, "", EnrichmentErrorTransient, 0, "fixture transient failure"); err != nil {
				t.Fatal(err)
			}
			for _, outcome := range tc.outcomes {
				if err := store.RecordOutcome(t.Context(), id, "", outcome); err != nil {
					t.Fatal(err)
				}
			}
			var attempts, noMatches int
			var next, done *time.Time
			var errorClass *string
			if err := pool.QueryRow(t.Context(), `SELECT attempts,no_matches,next_attempt_at,completed_at,last_error_class FROM audiobook_enrichment_state WHERE content_id=$1`, id).Scan(&attempts, &noMatches, &next, &done, &errorClass); err != nil {
				t.Fatal(err)
			}
			if attempts != tc.attempts || noMatches != tc.noMatches || (next != nil) != tc.parked || (done != nil) != tc.completed || errorClass != nil {
				t.Fatalf("attempts/no_matches/parked/completed/error=%d/%d/%t/%t/%v, want %d/%d/%t/%t/nil", attempts, noMatches, next != nil, done != nil, errorClass, tc.attempts, tc.noMatches, tc.parked, tc.completed)
			}
			if next != nil && !next.After(time.Now()) {
				t.Fatal("retry was not parked in the future")
			}
		})
	}
}
