package pgstore

import (
	"context"
	"errors"
)

// DiscardUnreceiptedAdmission removes only an exact, unused generation-one
// binding created without a first-admission receipt. This operator recovery is
// never invoked by discovery or start. Any durable playback evidence refuses it.
func (p *PostgresProvider) DiscardUnreceiptedAdmission(ctx context.Context, intent FirstAdmissionIntent, apply bool) (FirstAdmissionDecision, error) {
	decision := FirstAdmissionDecision{Intent: intent}
	if err := intent.Validate(); err != nil {
		return decision, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return decision, err
	}
	defer rollbackPlaybackSource(ctx, tx)
	if err = lockFirstAdmissionIdentity(ctx, tx, intent); err != nil {
		return decision, err
	}
	var occupied bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_v3_attempts WHERE user_id=$1) OR EXISTS(SELECT 1 FROM playback_progress_sinks WHERE user_id=$1) OR EXISTS(SELECT 1 FROM playback_first_admissions WHERE user_id=$1 OR intent_id=$2::uuid OR source_id=$3::uuid) OR EXISTS(SELECT 1 FROM playback_automatic_admission_intents WHERE user_id=$1)`, intent.AccountID, intent.IntentID, intent.SourceID).Scan(&occupied); err != nil {
		return decision, err
	}
	if occupied {
		return decision, errors.New("retained playback authority or intent prevents unreceipted cleanup")
	}
	var marker, registration, matching bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_source_markers WHERE user_id=$1),EXISTS(SELECT 1 FROM playback_source_registrations WHERE user_id=$1),EXISTS(SELECT 1 FROM playback_source_markers m JOIN playback_source_registrations r USING(user_id) WHERE m.user_id=$1 AND m.source_id=$2::uuid AND m.selection_generation=1 AND m.gate='writable' AND r.source_id=m.source_id AND r.backend='postgres' AND r.selection_generation=1 AND r.admission_id=$3::uuid AND r.admission_state='admitting')`, intent.AccountID, intent.SourceID, intent.IntentID).Scan(&marker, &registration, &matching); err != nil {
		return decision, err
	}
	if !marker && !registration {
		decision.State = "absent"
		return decision, nil
	}
	if !matching {
		return decision, errors.New("unreceipted cleanup requires the exact unused writable binding")
	}
	decision.State = "discardable"
	if !apply {
		return decision, nil
	}
	if _, err = tx.Exec(ctx, `DELETE FROM playback_source_registrations WHERE user_id=$1`, intent.AccountID); err != nil {
		return decision, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM playback_source_markers WHERE user_id=$1`, intent.AccountID); err != nil {
		return decision, err
	}
	if err = tx.Commit(ctx); err != nil {
		return FirstAdmissionDecision{Intent: intent, State: "unknown"}, err
	}
	decision.State = "discarded"
	return decision, nil
}
