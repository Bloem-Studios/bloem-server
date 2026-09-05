package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

var _ userstore.PlaybackProgressSink = (*PostgresUserStore)(nil)

const playbackSinkDocumentLimit = 256 * 1024

func (s *PostgresUserStore) ReadPlaybackProgress(ctx context.Context, scope userstore.PlaybackProgressScope) (userstore.PlaybackProgressState, error) {
	if err := scope.Validate(); err != nil {
		return userstore.PlaybackProgressState{}, err
	}
	state, err := loadPlaybackSink(ctx, s.pool, s.userID, scope, false)
	if err != nil {
		return userstore.PlaybackProgressState{}, err
	}
	if state == nil {
		return userstore.PlaybackProgressState{}, userstore.ErrPlaybackSinkNotFound
	}
	return *state, nil
}

func (s *PostgresUserStore) InstallPlaybackAuthority(ctx context.Context, request userstore.InstallPlaybackAuthorityRequest) (userstore.PlaybackProgressResult, error) {
	return s.mutatePlaybackSink(ctx, request.Scope, func(state *userstore.PlaybackProgressState) (userstore.PlaybackProgressChange, error) {
		return userstore.PreparePlaybackAuthority(state, request)
	})
}

func (s *PostgresUserStore) ApplyPlaybackProgress(ctx context.Context, request userstore.ApplyPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
	return s.mutatePlaybackSink(ctx, request.Scope, func(state *userstore.PlaybackProgressState) (userstore.PlaybackProgressChange, error) {
		return userstore.PreparePlaybackProgress(state, request)
	})
}

func (s *PostgresUserStore) StopPlaybackProgress(ctx context.Context, request userstore.StopPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
	return s.mutatePlaybackSink(ctx, request.Scope, func(state *userstore.PlaybackProgressState) (userstore.PlaybackProgressChange, error) {
		return userstore.PreparePlaybackStop(state, request)
	})
}

func (s *PostgresUserStore) mutatePlaybackSink(ctx context.Context, scope userstore.PlaybackProgressScope, prepare func(*userstore.PlaybackProgressState) (userstore.PlaybackProgressChange, error)) (userstore.PlaybackProgressResult, error) {
	var zero userstore.PlaybackProgressResult
	if err := scope.Validate(); err != nil {
		return zero, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return zero, fmt.Errorf("begin playback sink: %w", err)
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	if err := lockImportedHistory(ctx, tx, s.userID, scope.ProfileID); err != nil {
		return zero, err
	}
	state, err := loadPlaybackSink(ctx, tx, s.userID, scope, true)
	if err != nil {
		return zero, err
	}
	change, err := prepare(state)
	if err != nil {
		return zero, err
	}
	before, err := readPlaybackProjection(ctx, tx, s.userID, scope.ProfileID, scope.MediaItemID)
	if err != nil {
		return zero, err
	}
	change.Result.Before = playbackProjectionFact(before)
	if change.Changed {
		if change.Sample != nil {
			if err := s.projectPlaybackChange(ctx, tx, &change); err != nil {
				return zero, err
			}
		}
		if err := savePlaybackSink(ctx, tx, s.userID, change.Result.State); err != nil {
			return zero, err
		}
	}
	after, err := readPlaybackProjection(ctx, tx, s.userID, scope.ProfileID, scope.MediaItemID)
	if err != nil {
		return zero, err
	}
	change.Result.After = playbackProjectionFact(after)
	if err := tx.Commit(ctx); err != nil {
		return zero, fmt.Errorf("commit playback sink: %w", err)
	}
	return change.Result, nil
}

type playbackSinkQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadPlaybackSink(ctx context.Context, db playbackSinkQuerier, userID int, scope userstore.PlaybackProgressScope, lock bool) (*userstore.PlaybackProgressState, error) {
	query := `SELECT media_item_id,attempt_id,incarnation,owner_id,epoch,state,last_sequence,document FROM playback_progress_sinks WHERE user_id=$1 AND profile_id=$2 AND session_id=$3`
	if lock {
		query += ` FOR UPDATE`
	}
	var data []byte
	var target, status string
	var fence userstore.PlaybackProgressFence
	var sequence int64
	err := db.QueryRow(ctx, query, userID, scope.ProfileID, scope.SessionID).Scan(&target, &fence.AttemptID, &fence.Incarnation, &fence.OwnerID, &fence.Epoch, &status, &sequence, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read playback sink: %w", err)
	}
	if target != scope.MediaItemID {
		return nil, userstore.ErrPlaybackSinkStale
	}
	var state userstore.PlaybackProgressState
	if len(data) > playbackSinkDocumentLimit || json.Unmarshal(data, &state) != nil || state.Version != 1 || state.Scope != scope || state.Fence != fence ||
		(status == "stopped") != (state.Stop != nil) || (state.Last == nil && sequence != 0) || (state.Last != nil && sequence != state.Last.Sample.Sequence) {
		return nil, userstore.ErrPlaybackSinkInvalid
	}
	return &state, nil
}

func savePlaybackSink(ctx context.Context, tx pgx.Tx, userID int, state userstore.PlaybackProgressState) error {
	data, err := json.Marshal(state)
	if err != nil || len(data) > playbackSinkDocumentLimit {
		return userstore.ErrPlaybackSinkInvalid
	}
	status := "active"
	if state.Stop != nil {
		status = "stopped"
	}
	var sequence int64
	if state.Last != nil {
		sequence = state.Last.Sample.Sequence
	}
	_, err = tx.Exec(ctx, `INSERT INTO playback_progress_sinks(user_id,profile_id,session_id,media_item_id,attempt_id,incarnation,owner_id,epoch,state,last_sequence,document)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT(user_id,profile_id,session_id) DO UPDATE SET owner_id=excluded.owner_id,epoch=excluded.epoch,state=excluded.state,last_sequence=excluded.last_sequence,document=excluded.document`,
		userID, state.Scope.ProfileID, state.Scope.SessionID, state.Scope.MediaItemID, state.Fence.AttemptID, state.Fence.Incarnation, state.Fence.OwnerID, state.Fence.Epoch, status, sequence, data)
	if err != nil {
		return fmt.Errorf("save playback sink: %w", err)
	}
	return nil
}

func (s *PostgresUserStore) projectPlaybackChange(ctx context.Context, tx pgx.Tx, change *userstore.PlaybackProgressChange) error {
	scope, sample := change.Result.State.Scope, *change.Sample
	progress, hints, history := userstore.PlaybackProjectionPolicy(sample, change.Final)
	var at time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
		return err
	}
	position, completed, _ := userstore.ResolveProgressState(sample.PositionSeconds, sample.DurationSeconds, sample.Thresholds)
	if progress {
		if err := writePlaybackProjection(ctx, tx, s.userID, scope.ProfileID, scope.MediaItemID, position, sample.DurationSeconds, completed, at); err != nil {
			return err
		}
		change.Result.ProgressChanged = true
	}
	if hints {
		changed, err := writePlaybackHints(ctx, tx, s.userID, scope.ProfileID, scope.MediaItemID, sample.Hints)
		if err != nil {
			return err
		}
		change.Result.HintsChanged = changed
	}
	if history {
		entry := userstore.WatchHistoryEntry{ID: generateUUID(), ProfileID: scope.ProfileID, MediaItemID: scope.MediaItemID, WatchedAt: at.UTC().Format(time.RFC3339Nano), DurationSeconds: sample.DurationSeconds, Completed: completed, Source: userstore.WatchHistorySourcePlayback, Identity: change.Identity}
		entry, err := writePlaybackHistory(ctx, tx, s.userID, entry)
		if err != nil {
			return err
		}
		change.Result.HistoryCreated = true
		change.Result.State.Stop.History = &entry
	}
	return nil
}

func playbackProjectionFact(progress *userstore.WatchProgress) userstore.PlaybackProjectionState {
	if progress == nil {
		return userstore.PlaybackProjectionState{}
	}
	return userstore.PlaybackProjectionState{Exists: true, PositionSeconds: progress.PositionSeconds, Completed: progress.Completed}
}
