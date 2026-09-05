package userdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

var _ userstore.PlaybackProgressSink = (*SQLiteUserStore)(nil)

func (s *SQLiteUserStore) ReadPlaybackProgress(ctx context.Context, scope userstore.PlaybackProgressScope) (userstore.PlaybackProgressState, error) {
	if err := scope.Validate(); err != nil {
		return userstore.PlaybackProgressState{}, err
	}
	var state *userstore.PlaybackProgressState
	err := s.withPlaybackSinkTransaction(ctx, func(exec preferenceSettingsExecutor) error {
		var err error
		state, err = readPlaybackSink(ctx, exec, scope)
		return err
	})
	if err != nil {
		return userstore.PlaybackProgressState{}, err
	}

	if state == nil {
		return userstore.PlaybackProgressState{}, userstore.ErrPlaybackSinkNotFound
	}
	if state.Scope != scope {
		return userstore.PlaybackProgressState{}, userstore.ErrPlaybackSinkStale
	}
	return *state, nil
}

func (s *SQLiteUserStore) InstallPlaybackAuthority(ctx context.Context, request userstore.InstallPlaybackAuthorityRequest) (userstore.PlaybackProgressResult, error) {
	return s.mutatePlaybackSink(ctx, request.Scope, func(state *userstore.PlaybackProgressState) (userstore.PlaybackProgressChange, error) {
		return userstore.PreparePlaybackAuthority(state, request)
	})
}

func (s *SQLiteUserStore) ApplyPlaybackProgress(ctx context.Context, request userstore.ApplyPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
	return s.mutatePlaybackSink(ctx, request.Scope, func(state *userstore.PlaybackProgressState) (userstore.PlaybackProgressChange, error) {
		return userstore.PreparePlaybackProgress(state, request)
	})
}

func (s *SQLiteUserStore) StopPlaybackProgress(ctx context.Context, request userstore.StopPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
	return s.mutatePlaybackSink(ctx, request.Scope, func(state *userstore.PlaybackProgressState) (userstore.PlaybackProgressChange, error) {
		return userstore.PreparePlaybackStop(state, request)
	})
}

func (s *SQLiteUserStore) mutatePlaybackSink(ctx context.Context, scope userstore.PlaybackProgressScope, prepare func(*userstore.PlaybackProgressState) (userstore.PlaybackProgressChange, error)) (userstore.PlaybackProgressResult, error) {
	if err := scope.Validate(); err != nil {
		return userstore.PlaybackProgressResult{}, err
	}
	var result userstore.PlaybackProgressResult
	err := s.withPlaybackSinkTransaction(ctx, func(exec preferenceSettingsExecutor) error {
		state, err := readPlaybackSink(ctx, exec, scope)
		if err != nil {
			return err
		}
		change, err := prepare(state)
		if err != nil {
			return err
		}
		result = change.Result
		result.Before, err = readPlaybackProjection(ctx, exec, scope)
		if err != nil {
			return err
		}
		if change.Changed {
			if change.Sample != nil {
				sample := *change.Sample
				progress, hints, history := userstore.PlaybackProjectionPolicy(sample, change.Final)
				if progress {
					if err := setPlaybackProgress(exec, scope.ProfileID, scope.MediaItemID, sample.PositionSeconds, sample.DurationSeconds, sample.Thresholds); err != nil {
						return err
					}
					result.ProgressChanged = true
				}
				if hints {
					result.HintsChanged, err = updatePlaybackProgressHints(exec, scope.ProfileID, scope.MediaItemID, sample.Hints)
					if err != nil {
						return err
					}
				}
				if history {
					entry, err := addPlaybackVisibleHistory(exec, userstore.WatchHistoryEntry{
						ProfileID: scope.ProfileID, MediaItemID: scope.MediaItemID, DurationSeconds: sample.DurationSeconds,
						Completed: sample.DurationSeconds > 0 && sample.PositionSeconds/sample.DurationSeconds > userstore.WatchedFraction(sample.Thresholds.WatchedPct),
						Source:    userstore.WatchHistorySourcePlayback, Identity: change.Identity,
					})
					if err != nil {
						return err
					}
					result.State.Stop.History = &entry
					result.HistoryCreated = true
				}
			}
			if err := savePlaybackSink(ctx, exec, result.State); err != nil {
				return err
			}
		}
		result.After, err = readPlaybackProjection(ctx, exec, scope)
		return err
	})
	if err != nil {
		return userstore.PlaybackProgressResult{}, err
	}
	return result, nil
}

func readPlaybackProjection(ctx context.Context, exec preferenceSettingsExecutor, scope userstore.PlaybackProgressScope) (userstore.PlaybackProjectionState, error) {
	var projection userstore.PlaybackProjectionState
	err := exec.QueryRowContext(ctx, `SELECT position_seconds, completed FROM watch_progress wp
 WHERE profile_id=? AND media_item_id=? AND NOT EXISTS (
 SELECT 1 FROM hidden_history_items h WHERE h.profile_id=wp.profile_id AND h.media_item_id=wp.media_item_id AND wp.updated_at<=h.hidden_before)`, scope.ProfileID, scope.MediaItemID).Scan(&projection.PositionSeconds, &projection.Completed)
	if errors.Is(err, sql.ErrNoRows) {
		return projection, nil
	}
	projection.Exists = err == nil
	return projection, err
}

func readPlaybackSink(ctx context.Context, exec preferenceSettingsExecutor, scope userstore.PlaybackProgressScope) (*userstore.PlaybackProgressState, error) {
	var target, stateName, document string
	var fence userstore.PlaybackProgressFence
	var sequence int64
	err := exec.QueryRowContext(ctx, `SELECT media_item_id, attempt_id, incarnation, owner_id, epoch, state, last_sequence, document
 FROM playback_progress_sinks WHERE profile_id=? AND session_id=?`, scope.ProfileID, scope.SessionID).Scan(&target, &fence.AttemptID, &fence.Incarnation, &fence.OwnerID, &fence.Epoch, &stateName, &sequence, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(document) > 256*1024 {
		return nil, userstore.ErrPlaybackSinkInvalid
	}
	var state userstore.PlaybackProgressState
	if err := json.Unmarshal([]byte(document), &state); err != nil {
		return nil, fmt.Errorf("decode playback sink: %w", err)
	}
	expectedName, expectedSequence := playbackSinkMirrors(state)
	if state.Version != 1 || state.Scope.ProfileID != scope.ProfileID || state.Scope.SessionID != scope.SessionID || state.Scope.MediaItemID != target || state.Fence != fence || stateName != expectedName || sequence != expectedSequence {
		return nil, userstore.ErrPlaybackSinkInvalid
	}
	return &state, nil
}

func playbackSinkMirrors(state userstore.PlaybackProgressState) (string, int64) {
	name := "active"
	if state.Stop != nil {
		name = "stopped"
	}
	var sequence int64
	if state.Last != nil {
		sequence = state.Last.Sample.Sequence
	}
	return name, sequence
}

func savePlaybackSink(ctx context.Context, exec preferenceSettingsExecutor, state userstore.PlaybackProgressState) error {
	document, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(document) > 256*1024 {
		return userstore.ErrPlaybackSinkInvalid
	}
	name, sequence := playbackSinkMirrors(state)
	_, err = exec.ExecContext(ctx, `INSERT INTO playback_progress_sinks
 (profile_id,session_id,media_item_id,attempt_id,incarnation,owner_id,epoch,state,last_sequence,document)
 VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(profile_id,session_id) DO UPDATE SET
 media_item_id=excluded.media_item_id,attempt_id=excluded.attempt_id,incarnation=excluded.incarnation,
 owner_id=excluded.owner_id,epoch=excluded.epoch,state=excluded.state,last_sequence=excluded.last_sequence,document=excluded.document`,
		state.Scope.ProfileID, state.Scope.SessionID, state.Scope.MediaItemID, state.Fence.AttemptID, state.Fence.Incarnation, state.Fence.OwnerID, state.Fence.Epoch, name, sequence, string(document))
	return err
}

// Reserve the writer before reading receipts, on the exact selected database.
// No operation inside this callback may return to the connection pool.
func (s *SQLiteUserStore) withPlaybackSinkTransaction(ctx context.Context, fn func(preferenceSettingsExecutor) error) error {
	s.sourceMu.RLock()
	defer s.sourceMu.RUnlock()
	if s.sourceClosed {
		return userstore.ErrPlaybackSourceClosed
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	exec := settingMutationConnExecutor{ctx: ctx, conn: conn}
	if s.sourceRef != nil {
		if err := verifyPlaybackSourceDurability(ctx, exec); err != nil {
			return err
		}
	}
	if _, err := exec.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			if _, err := exec.ExecContext(rollbackCtx, "ROLLBACK"); err != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}
	}()
	if err := s.checkPlaybackSource(ctx, exec); err != nil {
		return err
	}
	if err := fn(exec); err != nil {
		return err
	}
	if _, err := exec.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}
