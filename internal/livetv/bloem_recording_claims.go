package livetv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// recordingClaimStore is the compare-and-swap surface the DVR recorder uses.
// Every write is conditional on the caller's claim token and on the row still
// being scheduled or recording, so a cancelled (or otherwise finished) row can
// never be resurrected and two replicas can never both own one recording.
// See the design note at the top of recorder.go.
type recordingClaimStore interface {
	// ClaimRecording takes id when it is in status and holds no live lease.
	// It returns nil, nil when another claimant holds it or the status moved.
	ClaimRecording(ctx context.Context, id, status, token, nodeID string, lease time.Duration) (*Recording, error)
	// MarkRecordingStarted records a running segment: status becomes
	// recording, segments is incremented, and a second or later segment marks
	// the recording interrupted.
	MarkRecordingStarted(ctx context.Context, id, token, path, tunerSessionID string, lease time.Duration) (bool, error)
	// RenewRecordingLease extends the owner's lease on a recording row.
	RenewRecordingLease(ctx context.Context, id, token string, lease time.Duration) (bool, error)
	// ReleaseRecordingClaim drops the claim without changing status so the
	// next tick (on any replica) retries or resumes it.
	ReleaseRecordingClaim(ctx context.Context, id, token, lastError string) (bool, error)
	// FinishRecordingClaim moves a claimed row to a terminal status.
	FinishRecordingClaim(ctx context.Context, id, token, status, path, lastError string) (bool, error)
}

var errRecordingClaimsUnsupported = errors.New("livetv store does not support recording claims")

func leaseSeconds(lease time.Duration) float64 { return lease.Seconds() }

func (s *PgStore) ClaimRecording(ctx context.Context, id, status, token, nodeID string, lease time.Duration) (*Recording, error) {
	rec, err := scanRecording(s.db.QueryRow(ctx, `
		UPDATE livetv_recordings
		SET claim_token = $3, claim_node_id = $4,
			lease_until = now() + make_interval(secs => $5), updated_at = now()
		WHERE id = $1 AND status = $2 AND (lease_until IS NULL OR lease_until < now())
		RETURNING `+recordingSelectCols, id, status, token, nodeID, leaseSeconds(lease)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim recording: %w", err)
	}
	return &rec, nil
}

func (s *PgStore) MarkRecordingStarted(ctx context.Context, id, token, path, tunerSessionID string, lease time.Duration) (bool, error) {
	return s.execClaimed(ctx, "mark recording started", `
		UPDATE livetv_recordings
		SET status = 'recording', path = $3, tuner_session_id = $4,
			interrupted = interrupted OR segments > 0, segments = segments + 1,
			last_error = '', lease_until = now() + make_interval(secs => $5), updated_at = now()
		WHERE id = $1 AND claim_token = $2 AND status IN ('scheduled', 'recording')`,
		id, token, path, tunerSessionID, leaseSeconds(lease))
}

func (s *PgStore) RenewRecordingLease(ctx context.Context, id, token string, lease time.Duration) (bool, error) {
	return s.execClaimed(ctx, "renew recording lease", `
		UPDATE livetv_recordings
		SET lease_until = now() + make_interval(secs => $3)
		WHERE id = $1 AND claim_token = $2 AND status = 'recording'`,
		id, token, leaseSeconds(lease))
}

func (s *PgStore) ReleaseRecordingClaim(ctx context.Context, id, token, lastError string) (bool, error) {
	return s.execClaimed(ctx, "release recording claim", `
		UPDATE livetv_recordings
		SET claim_token = '', lease_until = NULL, tuner_session_id = '',
			last_error = $3, start_attempts = start_attempts + 1, updated_at = now()
		WHERE id = $1 AND claim_token = $2 AND status IN ('scheduled', 'recording')`,
		id, token, lastError)
}

func (s *PgStore) FinishRecordingClaim(ctx context.Context, id, token, status, path, lastError string) (bool, error) {
	return s.execClaimed(ctx, "finish recording", `
		UPDATE livetv_recordings
		SET status = $3, path = COALESCE(NULLIF($4, ''), path), last_error = $5,
			claim_token = '', lease_until = NULL, tuner_session_id = '', updated_at = now()
		WHERE id = $1 AND claim_token = $2 AND status IN ('scheduled', 'recording')`,
		id, token, status, path, lastError)
}

func (s *PgStore) execClaimed(ctx context.Context, what, sqlText string, args ...any) (bool, error) {
	tag, err := s.db.Exec(ctx, sqlText, args...)
	if err != nil {
		return false, fmt.Errorf("%s: %w", what, err)
	}
	return tag.RowsAffected() == 1, nil
}

// claimRecordingTuner reserves a tuner index for a recording in the same
// livetv_sessions ledger live tunes use, so live viewers and the DVR see each
// other's usage. The partial unique index on (tuner_id, tuner_index) makes a
// concurrent claim lose with ErrTunerIndexConflict; retry once like live tunes.
func (s *Service) claimRecordingTuner(ctx context.Context, rec *Recording, channel *Channel) (*LiveSession, error) {
	tuner, err := s.store.GetTuner(ctx, channel.TunerID)
	if err != nil {
		return nil, err
	}
	if tuner == nil {
		return nil, fmt.Errorf("tuner %q for channel %q not found", channel.TunerID, channel.ID)
	}
	reclaimed := false
	for attempt := 0; attempt < 3; attempt++ {
		indices, err := s.store.ActiveSessionTunerIndices(ctx, tuner.ID)
		if err != nil {
			return nil, err
		}
		index, ok := firstFreeIndex(tuner.TunerCount, indices)
		if !ok {
			if reclaimed {
				return nil, ErrNoTuner
			}
			reclaimed = true
			if _, err := s.ReclaimStaleSessions(ctx); err != nil {
				return nil, err
			}
			continue
		}
		session, err := s.store.CreateSession(ctx, SessionCreate{
			ChannelID:         channel.ID,
			TunerID:           tuner.ID,
			TunerIndex:        index,
			UserID:            rec.UserID,
			ProfileID:         rec.ProfileID,
			PlaybackSessionID: recordingPlaybackID(rec.ID),
		})
		if err == nil {
			return session, nil
		}
		if !errors.Is(err, ErrTunerIndexConflict) {
			return nil, err
		}
	}
	return nil, ErrNoTuner
}

// recordingPlaybackID tags a DVR tuner session so it is distinguishable from a
// viewer's and so TouchSession can address it.
func recordingPlaybackID(recordingID string) string { return "dvr:" + recordingID }
