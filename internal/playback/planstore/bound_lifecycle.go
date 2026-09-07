package planstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/jackc/pgx/v5"
)

var _ playback.BoundPlaybackControlStoreV3 = (*Postgres)(nil)

func (s *Postgres) GetAdmittedPlaybackSource(ctx context.Context, accountID int) (playback.AdmittedPlaybackSourceV3, error) {
	var result playback.AdmittedPlaybackSourceV3
	if accountID <= 0 {
		return result, playback.ErrInitialActivationInvalidV3
	}
	result.Source.AccountID = accountID
	err := s.db.QueryRow(ctx, `SELECT backend,source_id::text,selection_generation,admission_id::text FROM playback_source_registrations WHERE user_id=$1 AND admission_state='admitting'`, accountID).Scan(&result.Source.Backend, &result.Source.SourceID, &result.Source.SelectionGeneration, &result.AdmissionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return playback.AdmittedPlaybackSourceV3{}, playback.ErrInitialActivationUnavailableV3
	}
	if err != nil {
		return playback.AdmittedPlaybackSourceV3{}, err
	}
	if err := result.Source.Validate(); err != nil {
		return playback.AdmittedPlaybackSourceV3{}, err
	}
	if !validInitialUUID(result.AdmissionID) {
		return playback.AdmittedPlaybackSourceV3{}, playback.ErrInitialActivationInvalidV3
	}
	return result, nil
}

func (s *Postgres) GetActivatedPlaybackAuthority(ctx context.Context, accountID int, profileID, sessionID string) (playback.ActivatedPlaybackAuthorityV3, error) {
	var zero playback.ActivatedPlaybackAuthorityV3
	if accountID <= 0 || profileID == "" || !validInitialUUID(sessionID) {
		return zero, playback.ErrInitialActivationInvalidV3
	}
	// This untrusted observation only discovers the exact binding. The locked
	// transaction below revalidates identity and phase before returning authority.
	var data []byte
	err := s.db.QueryRow(ctx, `SELECT control_activation FROM playback_v3_attempts WHERE user_id=$1 AND profile_id=$2 AND session_id=$3::uuid AND control_activation IS NOT NULL`, accountID, profileID, sessionID).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, playback.ErrSessionNotFound
	}
	if err != nil {
		return zero, err
	}
	var observed playback.InitialActivationV3
	if len(data) > initialActivationDocumentLimit {
		return zero, playback.ErrInitialActivationInvalidV3
	}
	if err := json.Unmarshal(data, &observed); err != nil {
		return zero, err
	}
	if err := validateStoredInitialActivation(observed); err != nil {
		return zero, err
	}
	if observed.Binding.Source.AccountID != accountID || observed.Binding.Scope.ProfileID != profileID || observed.Binding.Scope.SessionID != sessionID {
		return zero, playback.ErrInitialActivationConflictV3
	}
	var authority playback.AttemptAuthorityV3
	state, err := s.withInitialActivation(ctx, observed.Binding, func(_ pgx.Tx, row *initialActivationRow) (playback.InitialActivationV3, error) {
		if row.activation == nil {
			return playback.InitialActivationV3{}, playback.ErrInitialActivationConflictV3
		}
		switch row.activation.Phase {
		case playback.InitialActivationActivatedV3:
			if !row.admitting || !row.live() || row.authority.State != playback.AttemptActiveV3 {
				return playback.InitialActivationV3{}, playback.ErrInitialActivationConflictV3
			}
		case playback.InitialActivationStoppingV3:
			if row.authority.State != playback.AttemptDrainingV3 {
				return playback.InitialActivationV3{}, playback.ErrInitialActivationConflictV3
			}
		case playback.InitialActivationStoppedV3:
			if row.authority.State != playback.AttemptStoppedV3 {
				return playback.InitialActivationV3{}, playback.ErrInitialActivationConflictV3
			}
		default:
			return playback.InitialActivationV3{}, playback.ErrInitialActivationConflictV3
		}
		authority = row.authority
		return *row.activation, nil
	})
	if err != nil {
		return zero, err
	}
	return playback.ActivatedPlaybackAuthorityV3{Binding: state.Binding, Authority: authority, Activation: state}, nil
}

func (s *Postgres) BeginBoundStop(ctx context.Context, binding playback.InitialActivationBindingV3, stopID string) (playback.InitialActivationV3, error) {
	if stopID == "" {
		return playback.InitialActivationV3{}, playback.ErrInitialActivationInvalidV3
	}
	return s.withInitialActivation(ctx, binding, func(tx pgx.Tx, row *initialActivationRow) (playback.InitialActivationV3, error) {
		var zero playback.InitialActivationV3
		if row.activation == nil {
			return zero, playback.ErrInitialActivationConflictV3
		}
		state := *row.activation
		if state.Phase == playback.InitialActivationStoppingV3 || state.Phase == playback.InitialActivationStoppedV3 {
			if state.StopID != stopID {
				return zero, playback.ErrInitialActivationConflictV3
			}
			return state, nil
		}
		if state.Phase != playback.InitialActivationActivatedV3 || row.authority.State != playback.AttemptActiveV3 || !row.live() {
			return zero, playback.ErrInitialActivationConflictV3
		}
		state.Phase = playback.InitialActivationStoppingV3
		state.StopID = stopID
		state.DrainNotBefore = row.now
		if row.grantNotAfter != nil && row.grantNotAfter.After(state.DrainNotBefore) {
			state.DrainNotBefore = *row.grantNotAfter
		}
		if _, err := tx.Exec(ctx, `UPDATE playback_v3_attempts SET control_state='draining',control_drain_not_before=$2 WHERE playback_attempt_id=$1`, binding.Fence.AttemptID, state.DrainNotBefore); err != nil {
			return zero, err
		}
		return state, saveInitialActivation(ctx, tx, state)
	})
}

func (s *Postgres) CompleteBoundStop(ctx context.Context, binding playback.InitialActivationBindingV3, stopID string, observed playback.InitialActivationReceiptV3) (playback.InitialActivationV3, error) {
	receipt, err := observed.StateFor(binding)
	if err != nil {
		return playback.InitialActivationV3{}, err
	}
	if err := playback.ValidateInitialTerminalV3(binding, receipt); err != nil {
		return playback.InitialActivationV3{}, err
	}
	if receipt.Stop.StopID != stopID {
		return playback.InitialActivationV3{}, playback.ErrInitialActivationConflictV3
	}
	return s.withInitialActivation(ctx, binding, func(tx pgx.Tx, row *initialActivationRow) (playback.InitialActivationV3, error) {
		var zero playback.InitialActivationV3
		if row.activation == nil {
			return zero, playback.ErrInitialActivationConflictV3
		}
		state := *row.activation
		if state.StopID != stopID || (state.Phase != playback.InitialActivationStoppingV3 && state.Phase != playback.InitialActivationStoppedV3) || state.DrainNotBefore.After(row.now) {
			return zero, playback.ErrInitialActivationConflictV3
		}
		if state.Phase == playback.InitialActivationStoppedV3 {
			if !reflect.DeepEqual(state.Terminal, &receipt) {
				return zero, playback.ErrInitialActivationConflictV3
			}
			return state, nil
		}
		state.Phase = playback.InitialActivationStoppedV3
		state.Terminal = &receipt
		if _, err := tx.Exec(ctx, `UPDATE playback_v3_attempts SET control_state='stopped' WHERE playback_attempt_id=$1`, binding.Fence.AttemptID); err != nil {
			return zero, err
		}
		return state, saveInitialActivation(ctx, tx, state)
	})
}

// SessionActivationPhase reads the durable activation phase and owning
// account/profile of a session's attempt row without the caller's binding,
// for a session no longer held by any manager. found is false when no
// attempt row exists.
func (s *Postgres) SessionActivationPhase(ctx context.Context, sessionID string) (playback.SessionActivationPhaseV3, bool, error) {
	var out playback.SessionActivationPhaseV3
	var phase *string
	err := s.db.QueryRow(ctx, `SELECT user_id, profile_id, control_activation->>'phase' FROM playback_v3_attempts WHERE session_id=$1::uuid`, sessionID).Scan(&out.UserID, &out.ProfileID, &phase)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if phase != nil {
		out.Phase = playback.InitialActivationPhaseV3(*phase)
	}
	return out, true, nil
}
