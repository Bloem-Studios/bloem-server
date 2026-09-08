package pgstore

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AutomaticFirstAdmission retains an intent before applying the same transition
// used by explicit first admission. A lost commit response is resolved by reading
// the retained intent; neither retries nor competing API nodes choose new IDs.
func (p *PostgresProvider) AutomaticFirstAdmission(ctx context.Context, accountID int) (FirstAdmissionDecision, error) {
	intent, err := p.retainAutomaticFirstAdmission(ctx, accountID)
	if err != nil {
		return FirstAdmissionDecision{}, err
	}
	return p.FirstAdmission(ctx, intent, true)
}

func (p *PostgresProvider) retainAutomaticFirstAdmission(ctx context.Context, accountID int) (FirstAdmissionIntent, error) {
	var intent FirstAdmissionIntent
	if p == nil || p.pool == nil || accountID <= 0 {
		return intent, userstore.ErrPlaybackSourceUnavailable
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return intent, err
	}
	defer rollbackPlaybackSource(ctx, tx)
	// Use the transition's lock order, including identity and configuration writers.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('silo:server_settings:mutation',0))`); err != nil {
		return intent, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('playback-source:'||$1::integer::text,0))`, accountID); err != nil {
		return intent, err
	}
	intent.AccountID = accountID
	if err = tx.QueryRow(ctx, `SELECT username FROM users WHERE id=$1 FOR UPDATE`, accountID).Scan(&intent.ExpectedUsername); err != nil {
		return intent, err
	}
	if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT value FROM server_settings WHERE key='diagnostics.server_instance_id'),''), COALESCE(NULLIF((SELECT value FROM server_settings WHERE key='userdb.backend'),''),'postgres')`).Scan(&intent.InstallationID, &intent.Backend); err != nil {
		return intent, err
	}
	retained := FirstAdmissionIntent{AccountID: accountID, Backend: "postgres"}
	err = tx.QueryRow(ctx, `SELECT installation_id::text,expected_username,source_id::text,intent_id::text FROM playback_automatic_admission_intents WHERE user_id=$1`, accountID).Scan(&retained.InstallationID, &retained.ExpectedUsername, &retained.SourceID, &retained.IntentID)
	if errors.Is(err, pgx.ErrNoRows) {
		var occupied bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_source_markers WHERE user_id=$1) OR EXISTS(SELECT 1 FROM playback_source_registrations WHERE user_id=$1) OR EXISTS(SELECT 1 FROM playback_v3_attempts WHERE user_id=$1) OR EXISTS(SELECT 1 FROM playback_progress_sinks WHERE user_id=$1) OR EXISTS(SELECT 1 FROM playback_first_admissions WHERE user_id=$1)`, accountID).Scan(&occupied); err != nil {
			return intent, err
		}
		if occupied {
			return intent, userstore.ErrPlaybackSourceUnavailable
		}

		intent.SourceID, intent.IntentID = uuid.NewString(), uuid.NewString()
		if err = intent.Validate(); err != nil {
			return intent, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO playback_automatic_admission_intents(user_id,installation_id,expected_username,source_id,intent_id) VALUES($1,$2,$3,$4,$5)`, accountID, intent.InstallationID, intent.ExpectedUsername, intent.SourceID, intent.IntentID); err != nil {
			return intent, err
		}
		retained = intent
	} else if err != nil {
		return intent, err
	}
	if intent.Backend != "postgres" {
		return intent, userstore.ErrPlaybackSourceUnavailable
	}
	if retained.InstallationID != intent.InstallationID || retained.ExpectedUsername != intent.ExpectedUsername {
		return intent, errors.New("automatic first-admission identity changed")
	}
	if err = tx.Commit(ctx); err != nil {
		return FirstAdmissionIntent{}, err
	}
	return retained, nil
}
