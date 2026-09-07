package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FirstAdmissionIntent must be retained before apply. No identities are generated
// by the service, including after an uncertain commit. This is first enrollment
// of the current PostgreSQL source, never source selection, restore or cutover.
type FirstAdmissionIntent struct {
	InstallationID   string `json:"installation_id"`
	AccountID        int    `json:"account_id"`
	ExpectedUsername string `json:"expected_username"`
	Backend          string `json:"backend"`
	SourceID         string `json:"source_id"`
	IntentID         string `json:"intent_id"`
}

type FirstAdmissionDecision struct {
	Intent     FirstAdmissionIntent `json:"intent"`
	State      string               `json:"state"`
	AdmittedAt time.Time            `json:"admitted_at,omitzero"`
}

func (i FirstAdmissionIntent) Validate() error {
	if i.AccountID <= 0 || i.ExpectedUsername == "" || i.Backend != "postgres" {
		return errors.New("an exact existing account and PostgreSQL backend are required")
	}
	for _, value := range []string{i.InstallationID, i.SourceID, i.IntentID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return errors.New("canonical nonzero installation, source and intent UUIDs are required")
		}
	}
	return nil
}

// FirstAdmission defaults to inspection when apply is false. Both modes inspect
// under the same locks; only apply commits a marker, registration and receipt.
// Before the exclusive gate, old playback-origin writes may finish. After its
// commit, even delayed callbacks refuse. Existing attempt/sink rows refuse first
// admission; this command never deletes, adopts or blesses previous authority.
func (p *PostgresProvider) FirstAdmission(ctx context.Context, intent FirstAdmissionIntent, apply bool) (FirstAdmissionDecision, error) {
	decision := FirstAdmissionDecision{Intent: intent}
	if err := intent.Validate(); err != nil {
		return decision, err
	}
	if p == nil || p.pool == nil {
		return decision, userstore.ErrPlaybackSourceUnavailable
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return decision, err
	}
	defer rollbackPlaybackSource(ctx, tx)
	// Matches ServerSettingsRepo writers and serializes absence of the default
	// backend setting too. Never seed an installation identity as a side effect.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('silo:server_settings:mutation',0))`); err != nil {
		return decision, err
	}
	var installation, backend string
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT value FROM server_settings WHERE key='diagnostics.server_instance_id'),''), COALESCE(NULLIF((SELECT value FROM server_settings WHERE key='userdb.backend'),''),'postgres')`).Scan(&installation, &backend); err != nil {
		return decision, err
	}
	if installation != intent.InstallationID || backend != "postgres" {
		return decision, errors.New("installation or configured provider differs from retained intent")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('playback-source:'||$1::integer::text,0))`, intent.AccountID); err != nil {
		return decision, err
	}
	var username string
	if err := tx.QueryRow(ctx, `SELECT username FROM users WHERE id=$1 FOR UPDATE`, intent.AccountID).Scan(&username); err != nil {
		return decision, err
	}
	if username != intent.ExpectedUsername {
		return decision, errors.New("account identity differs from retained intent")
	}
	var retained FirstAdmissionIntent
	retained.Backend = "postgres"
	err = tx.QueryRow(ctx, `SELECT user_id,installation_id::text,intent_id::text,source_id::text,expected_username,admitted_at FROM playback_first_admissions WHERE user_id=$1`, intent.AccountID).Scan(&retained.AccountID, &retained.InstallationID, &retained.IntentID, &retained.SourceID, &retained.ExpectedUsername, &decision.AdmittedAt)
	if err == nil {
		if retained != intent {
			return decision, errors.New("a different first-admission decision is retained")
		}
		var matching bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_source_markers m JOIN playback_source_registrations r USING(user_id) WHERE m.user_id=$1 AND m.source_id=$2::uuid AND m.selection_generation=1 AND m.gate='writable' AND r.backend='postgres' AND r.source_id=m.source_id AND r.selection_generation=1 AND r.admission_id=$3::uuid AND r.admission_state='admitting')`, intent.AccountID, intent.SourceID, intent.IntentID).Scan(&matching); err != nil {
			return decision, err
		}
		if !matching {
			return decision, errors.New("retained decision no longer matches the exact admitted source; refusing repair")
		}
		decision.State = "already_admitted"
		return decision, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return decision, err
	}
	var occupied bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_source_markers WHERE user_id=$1) OR EXISTS(SELECT 1 FROM playback_source_registrations WHERE user_id=$1) OR EXISTS(SELECT 1 FROM playback_v3_attempts WHERE user_id=$1) OR EXISTS(SELECT 1 FROM playback_progress_sinks WHERE user_id=$1) OR EXISTS(SELECT 1 FROM playback_first_admissions WHERE intent_id=$2::uuid OR source_id=$3::uuid)`, intent.AccountID, intent.IntentID, intent.SourceID).Scan(&occupied); err != nil {
		return decision, err
	}
	if occupied {
		return decision, errors.New("source, intent, playback attempt or sink authority already exists; first admission refuses adoption")
	}
	decision.State = "eligible"
	if !apply {
		return decision, nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO playback_source_markers(user_id,source_id,selection_generation,gate) VALUES($1,$2,1,'writable')`, intent.AccountID, intent.SourceID); err != nil {
		return decision, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO playback_source_registrations(user_id,backend,source_id,selection_generation,admission_id,admission_state) VALUES($1,'postgres',$2,1,$3,'admitting')`, intent.AccountID, intent.SourceID, intent.IntentID); err != nil {
		return decision, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO playback_first_admissions(user_id,installation_id,intent_id,source_id,expected_username) VALUES($1,$2,$3,$4,$5) RETURNING admitted_at`, intent.AccountID, intent.InstallationID, intent.IntentID, intent.SourceID, intent.ExpectedUsername).Scan(&decision.AdmittedAt); err != nil {
		return decision, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FirstAdmissionDecision{Intent: intent, State: "unknown"}, fmt.Errorf("first-admission commit uncertain; inspect using the unchanged retained intent: %w", err)
	}
	decision.State = "admitted"
	return decision, nil
}
