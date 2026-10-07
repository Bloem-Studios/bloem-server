//go:build integration

package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Only the one progress table is present on fileless matched L. Other child
// evidence cannot cause an earlier refusal and hide either conflict-age branch.
func TestNativeOnboardingProgressAgeConflictDB(t *testing.T) {
	pool := metadataCurrentDatabase(t)
	native, _ := metadataNativeItem(t, pool)
	user, profile, _ := metadataCurrentProfile(t, pool)
	for _, age := range []string{"sourceOlder", "sourceNewer"} {
		t.Run(age, func(t *testing.T) {
			local := "progress-local-" + uuid.NewString()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := pool.Exec(ctx, "DELETE FROM user_watch_progress WHERE media_item_id=ANY($1::text[])", []string{local, native}); err != nil {
					t.Error("progress subtest cleanup", err)
				}
			})
			metadataExec(t, pool, "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'ebook','matched','Fileless local progress')", local)
			now := time.Now().UTC().Truncate(time.Microsecond)
			sourceTime := now.Add(-time.Hour)
			if age == "sourceNewer" {
				sourceTime = now.Add(time.Hour)
			}
			metadataExec(t, pool, "INSERT INTO user_watch_progress(user_id,profile_id,media_item_id,position_seconds,duration_seconds,updated_at) VALUES($1,$2,$3,20,100,$4),($1,$2,$5,60,100,$6)", user, profile, local, sourceTime, native, now)
			query := "SELECT jsonb_agg(to_jsonb(p) ORDER BY media_item_id)::text FROM user_watch_progress p WHERE media_item_id=ANY($1::text[])"
			var before string
			if err := pool.QueryRow(t.Context(), query, []string{local, native}).Scan(&before); err != nil {
				t.Fatal(err)
			}
			err := (&MetadataService{dbPool: pool}).rebindItemToExistingItem(t.Context(), local, native, false)
			var after string
			if queryErr := pool.QueryRow(t.Context(), query, []string{local, native}).Scan(&after); queryErr != nil {
				t.Fatal(queryErr)
			}
			t.Logf("actual private false-flag %s err=%T %v progressStateRetained=%t", age, err, err, after == before)
			metadataState(t, err, "BN001")
			if after != before {
				t.Fatal("actual progress collision did not roll back source/target rows")
			}
		})
	}
}
