package planstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var _ playback.OwnerLossRecoveryStoreV3 = (*Postgres)(nil)

func (s *Postgres) LookupInitialRecovery(ctx context.Context, q playback.InitialRecoveryLookupV3) (playback.InitialActivationV3, error) {
	var zero playback.InitialActivationV3
	if q.AccountID <= 0 || q.ProfileID == "" || (q.AttemptID == "" && q.SessionID == "") || (q.AttemptID != "" && q.RequestDigest == "") || (q.SessionID != "" && !validInitialUUID(q.SessionID)) {
		return zero, playback.ErrInitialActivationInvalidV3
	}
	var data []byte
	err := s.db.QueryRow(ctx, `SELECT control_activation FROM playback_v3_attempts WHERE user_id=$1 AND profile_id=$2 AND ($3='' OR playback_attempt_id=$3) AND ($4='' OR session_id=NULLIF($4,'')::uuid) AND control_activation IS NOT NULL`, q.AccountID, q.ProfileID, q.AttemptID, q.SessionID).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, playback.ErrSessionNotFound
	}
	if err != nil {
		return zero, err
	}
	if len(data) > initialActivationDocumentLimit {
		return zero, playback.ErrInitialActivationInvalidV3
	}
	if err = json.Unmarshal(data, &zero); err != nil {
		return playback.InitialActivationV3{}, err
	}
	if err = validateStoredInitialActivation(zero); err != nil {
		return playback.InitialActivationV3{}, err
	}
	return s.withInitialActivation(ctx, zero.Binding, func(tx pgx.Tx, row *initialActivationRow) (playback.InitialActivationV3, error) {
		if !row.admitting || row.activation == nil || row.activation.Binding.Source.AccountID != q.AccountID || row.activation.Binding.Scope.ProfileID != q.ProfileID {
			return playback.InitialActivationV3{}, playback.ErrInitialActivationConflictV3
		}
		if q.RequestDigest != "" {
			var digest string
			if err := tx.QueryRow(ctx, `SELECT request_digest FROM playback_v3_attempts WHERE playback_attempt_id=$1`, zero.Binding.Fence.AttemptID).Scan(&digest); err != nil {
				return playback.InitialActivationV3{}, err
			}
			if digest != q.RequestDigest {
				return playback.InitialActivationV3{}, playback.ErrIdempotencyKeyReusedV3
			}
		}
		return *row.activation, nil
	})
}

// BeginOwnerLossRecovery never advances the old fence or competes with a
// persisted client StopID. UUID creation happens only in the winning locked CAS.
func (s *Postgres) BeginOwnerLossRecovery(ctx context.Context, b playback.InitialActivationBindingV3) (playback.InitialActivationV3, error) {
	return s.withInitialActivation(ctx, b, func(tx pgx.Tx, row *initialActivationRow) (playback.InitialActivationV3, error) {
		var zero playback.InitialActivationV3
		if !row.admitting || row.activation == nil {
			return zero, playback.ErrInitialActivationConflictV3
		}
		state := *row.activation
		if state.AbortReason == playback.InitialAbortOwnerLostV3 {
			return state, nil
		}
		if state.Phase == playback.InitialActivationStoppingV3 || state.Phase == playback.InitialActivationStoppedV3 {
			return state, nil
		}
		if row.live() {
			return zero, playback.ErrInitialOwnerLiveV3
		}
		if state.Phase != playback.InitialActivationPendingV3 && state.Phase != playback.InitialActivationInstalledV3 && state.Phase != playback.InitialActivationActivatedV3 {
			return zero, playback.ErrInitialActivationConflictV3
		}
		state.Phase = playback.InitialActivationAbortingV3
		state.AbortReason = playback.InitialAbortOwnerLostV3
		state.AbortID = uuid.NewString()
		state.DrainNotBefore = row.now
		if row.grantNotAfter != nil && row.grantNotAfter.After(state.DrainNotBefore) {
			state.DrainNotBefore = *row.grantNotAfter
		}
		var replacementDeadline *time.Time
		if err := tx.QueryRow(ctx, `SELECT MAX(NULLIF(route_replacement->>'drain_not_before','')::timestamptz) FROM playback_v3_replans WHERE session_id=$1::uuid`, b.Scope.SessionID).Scan(&replacementDeadline); err != nil {
			return zero, err
		}
		if replacementDeadline != nil && replacementDeadline.After(state.DrainNotBefore) {
			state.DrainNotBefore = *replacementDeadline
		}
		// Candidate/output/auxiliary issuance shares this aggregate and takes this
		// attempt lock. Clearing no permit or route can reduce the drain deadline.
		if _, err := tx.Exec(ctx, `UPDATE playback_v3_attempts SET control_state='draining',control_drain_not_before=$2 WHERE playback_attempt_id=$1`, b.Fence.AttemptID, state.DrainNotBefore); err != nil {
			return zero, err
		}
		return state, saveInitialActivation(ctx, tx, state)
	})
}

// Called under the source-registration lock, before a fresh reservation insert.
// Other live sessions remain legal; only lost-owner terminal work blocks admission.
func checkOwnerLossAdmission(ctx context.Context, tx pgx.Tx, r playback.AttemptReservationRequestV3) error {
	if r.ExpectedAdmissionID == "" {
		return nil
	}
	var pending bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_v3_attempts WHERE user_id=$1 AND profile_id=$2 AND playback_attempt_id<>$3 AND control_activation IS NOT NULL AND control_activation->'binding'->>'admission_id'=$4 AND ((control_activation->>'phase' IN ('pending','installed','activated') AND (control_lease_expires_at<=clock_timestamp() OR expires_at<=clock_timestamp())) OR (control_activation->>'phase'='aborting' AND control_activation->>'abort_reason'='owner_lost') OR (control_activation->>'phase'='stopping' AND control_lease_expires_at<=clock_timestamp())))`, r.UserID, r.ProfileID, r.PlaybackAttemptID, r.ExpectedAdmissionID).Scan(&pending)
	if err != nil {
		return err
	}
	if pending {
		return playback.ErrPlaybackRecoveryPendingV3
	}
	return nil
}
