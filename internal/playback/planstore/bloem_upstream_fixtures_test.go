package planstore

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They are copied unchanged so the Silo files stay deleted.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/jackc/pgx/v5/pgxpool"
)

// planstoreFixture holds the minimal FK graph (user + media files) that
// playback_v3_attempts and playback_route_events rows require.
type planstoreFixture struct {
	pool        *pgxpool.Pool
	userID      int
	mediaFileID int
	altFileID   int
}

// newPlanstoreFixture connects to SILO_TEST_DATABASE_URL (skipping when
// unset), verifies the v3 migrations are applied, and inserts the fixture
// rows every attempt/event insert depends on. Cleanup deletes everything the
// tests wrote so reruns against the same database stay green.
func newPlanstoreFixture(t *testing.T) *planstoreFixture {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)

	var tableName *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.playback_v3_attempts')::text`).Scan(&tableName); err != nil {
		t.Fatalf("check playback_v3_attempts table: %v", err)
	}
	if tableName == nil || *tableName == "" {
		t.Skip("test database has not applied the playback protocol v3 migration")
	}
	var hasRevision bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'playback_v3_attempts' AND column_name = 'current_replan_request_id'
		)`).Scan(&hasRevision); err != nil {
		t.Fatalf("check current_replan_request_id column: %v", err)
	}
	if !hasRevision {
		t.Skip("test database has not applied the playback v3 attempt revision migration")
	}
	var hasFrozenRecipe bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'playback_v3_attempts' AND column_name = 'frozen_recipe'
		)`).Scan(&hasFrozenRecipe); err != nil {
		t.Fatalf("check frozen_recipe column: %v", err)
	}
	if !hasFrozenRecipe {
		t.Skip("test database has not applied the playback v3 frozen recipe migration")
	}
	var hasStartResponse bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'playback_v3_attempts' AND column_name = 'start_response'
		)`).Scan(&hasStartResponse); err != nil {
		t.Fatalf("check start_response column: %v", err)
	}
	if !hasStartResponse {
		t.Skip("test database has not applied the terminal playback v3 attempt migration")
	}

	f := &planstoreFixture{pool: pool}
	unique := fmt.Sprintf("planstore-test-%d", time.Now().UnixNano())

	var folderID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_folders (type, name) VALUES ('movies', $1) RETURNING id`, unique).Scan(&folderID); err != nil {
		t.Fatalf("insert fixture media folder: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, role) VALUES ($1, 'user') RETURNING id`, unique).Scan(&f.userID); err != nil {
		t.Fatalf("insert fixture user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_files (media_folder_id, file_path) VALUES ($1, $2) RETURNING id`,
		folderID, "/fixtures/"+unique+"/movie.mkv").Scan(&f.mediaFileID); err != nil {
		t.Fatalf("insert fixture media file: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_files (media_folder_id, file_path) VALUES ($1, $2) RETURNING id`,
		folderID, "/fixtures/"+unique+"/movie-alt.mkv").Scan(&f.altFileID); err != nil {
		t.Fatalf("insert alternate fixture media file: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Attempts cascade to replans; deleting the user cascades any attempt
		// or route event a failed subtest left behind; the folder cascades the
		// media files.
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM playback_route_events WHERE user_id = $1`, f.userID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM playback_v3_attempts WHERE user_id = $1`, f.userID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, f.userID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM media_folders WHERE id = $1`, folderID)
	})
	return f
}

func (f *planstoreFixture) attemptRecord(sessionID, attemptID, digest string) playback.AttemptRecordV3 {
	record := playback.AttemptRecordV3{
		PlaybackAttemptID:      attemptID,
		SessionID:              sessionID,
		UserID:                 f.userID,
		ProfileID:              "profile-1",
		RequestedMediaFileID:   f.mediaFileID,
		EffectiveMediaFileID:   f.mediaFileID,
		CurrentPlanID:          "plan-1",
		CurrentReplanRequestID: "",
		CurrentPlan: playback.PlanV3{
			ProtocolVersion:      3,
			PlanID:               "plan-1",
			SessionID:            sessionID,
			DecisionReason:       "direct_play",
			RequestedMediaFileID: f.mediaFileID,
			EffectiveMediaFileID: f.mediaFileID,
		},
		FrozenRecipe: playback.ExecutableRecipeV3{
			Version: 1, PlanID: "plan-1", PlayMethod: playback.PlayDirect,
			SubtitleTrackIndex: -1, SubtitleTransportTrackIndex: -1,
		},
		NormalizedRequest: playback.StartRequestV3{
			ProtocolVersion:   3,
			FileID:            f.mediaFileID,
			ProfileID:         "profile-1",
			PlaybackAttemptID: attemptID,
			QualityPreference: "auto",
		},
		RequestDigest: digest,
		ExpiresAt:     time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond),
	}
	record.StartResponse = playback.DecisionResponseV3{
		ProtocolVersion: playback.ProtocolV3,
		ServerFeatures:  playback.ServerFeaturesV3(),
		Outcome:         playback.OutcomePlayableV3,
		SessionID:       sessionID,
		PlaybackPlan:    &record.CurrentPlan,
	}
	return record
}

func (f *planstoreFixture) expireAttempt(t *testing.T, attemptID string) {
	t.Helper()
	tag, err := f.pool.Exec(context.Background(), `
		UPDATE playback_v3_attempts SET expires_at = NOW() - INTERVAL '1 minute'
		WHERE playback_attempt_id = $1`, attemptID)
	if err != nil {
		t.Fatalf("expire attempt %s: %v", attemptID, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expire attempt %s: affected %d rows", attemptID, tag.RowsAffected())
	}
}
