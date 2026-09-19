package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/jackc/pgx/v5"
)

const xtreamLeaseTTL = 60 * time.Second

var errXtreamDuplicate = errors.New("xtream account is already configured at this provider URL")

func xtreamCredentialsAAD(tuner *Tuner) string {
	return secret.RowAAD("bloem_livetv_xtream_credentials", "credentials", tuner.ID) + ":" + tuner.BaseURL
}

func decryptXtreamCredentials(cipher *secret.Cipher, tuner *Tuner, envelope string) (xtreamCredentials, error) {
	var credentials xtreamCredentials
	if cipher == nil || !secret.IsEncrypted(envelope) {
		return credentials, ErrNotConfigured
	}
	plain, err := cipher.Decrypt(envelope, xtreamCredentialsAAD(tuner))
	if err != nil || json.Unmarshal([]byte(plain), &credentials) != nil {
		return credentials, errors.New("Xtream provider credentials could not be opened")
	}
	return credentials, nil
}

func (s *PgStore) createXtreamTuner(ctx context.Context, tuner *Tuner, credentials xtreamCredentials, cipher *secret.Cipher, channels []Channel) error {
	if cipher == nil || tuner.Type != TunerTypeXtream || tuner.ID == "" || tuner.TunerCount < 1 || tuner.TunerCount > 64 {
		return ErrInvalidArgument
	}
	plain, err := json.Marshal(credentials)
	if err != nil {
		return errors.New("Xtream credentials could not be encoded")
	}
	envelope, err := cipher.Encrypt(string(plain), xtreamCredentialsAAD(tuner))
	if err != nil {
		return errors.New("Xtream credentials could not be encrypted")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // transaction also rolls back on connection close
	// Serialize provider creation without storing a guessable credential hash
	// or username outside the authenticated encryption envelope. No network I/O
	// occurs while this lock is held. 0x5854524d is the Xtream lock namespace.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(1481921101,1)`); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM livetv_tuners WHERE type='xtream'`).Scan(&count); err != nil {
		return err
	}
	if count >= 64 {
		return fmt.Errorf("%w: Xtream provider limit reached", ErrLimitExceeded)
	}
	rows, err := tx.Query(ctx, `SELECT t.id,t.base_url,x.credentials FROM livetv_tuners t LEFT JOIN bloem_livetv_xtream_credentials x ON x.tuner_id=t.id WHERE t.type='xtream' AND t.base_url=$1`, tuner.BaseURL)
	if err != nil {
		return err
	}
	type existingCredential struct{ id, base, envelope string }
	existing, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (existingCredential, error) {
		var value existingCredential
		err := row.Scan(&value.id, &value.base, &value.envelope)
		return value, err
	})
	if err != nil {
		return err
	}
	for _, value := range existing {
		prior, err := decryptXtreamCredentials(cipher, &Tuner{ID: value.id, BaseURL: value.base}, value.envelope)
		if err != nil {
			return err
		}
		if prior.Username == credentials.Username {
			return errXtreamDuplicate
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO livetv_tuners(id,type,device_id,base_url,model,tuner_count,status,last_scan_at) VALUES($1,'xtream',$1,$2,$3,$4,'ready',clock_timestamp())`, tuner.ID, tuner.BaseURL, tuner.Model, tuner.TunerCount); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO bloem_livetv_xtream_credentials(tuner_id,credentials) VALUES($1,$2)`, tuner.ID, envelope); err != nil {
		return err
	}
	values := make([][]any, 0, len(channels))
	for i, ch := range channels {
		values = append(values, []any{ch.ID, tuner.ID, ch.Number, ch.Name, ch.StreamURL, ch.GuideStationID, true, i})
	}
	if _, err = tx.CopyFrom(ctx, pgx.Identifier{"livetv_channels"}, []string{"id", "tuner_id", "number", "name", "stream_url", "guide_station_id", "enabled", "sort_key"}, pgx.CopyFromRows(values)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) loadXtreamCredentials(ctx context.Context, tuner *Tuner, cipher *secret.Cipher) (xtreamCredentials, error) {
	var envelope string
	if tuner.Type != TunerTypeXtream {
		return xtreamCredentials{}, ErrInvalidArgument
	}
	err := s.db.QueryRow(ctx, `SELECT credentials FROM bloem_livetv_xtream_credentials WHERE tuner_id=$1`, tuner.ID).Scan(&envelope)
	if errors.Is(err, pgx.ErrNoRows) {
		return xtreamCredentials{}, ErrNotFound
	}
	if err != nil {
		return xtreamCredentials{}, err
	}
	return decryptXtreamCredentials(cipher, tuner, envelope)
}

func (s *PgStore) claimXtreamLease(ctx context.Context, tunerID, leaseID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback is best effort on returned error
	var capacity int
	err = tx.QueryRow(ctx, `SELECT tuner_count FROM livetv_tuners WHERE id=$1 AND type='xtream' FOR UPDATE`, tunerID).Scan(&capacity)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM bloem_livetv_xtream_leases WHERE tuner_id=$1 AND expires_at<=clock_timestamp()`, tunerID); err != nil {
		return err
	}
	var used int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM bloem_livetv_xtream_leases WHERE tuner_id=$1`, tunerID).Scan(&used); err != nil {
		return err
	}
	if capacity < 1 || used >= capacity {
		return fmt.Errorf("%w: Xtream connection capacity reached", ErrLimitExceeded)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO bloem_livetv_xtream_leases(lease_id,tuner_id,expires_at) VALUES($1,$2,clock_timestamp()+$3*interval '1 second')`, leaseID, tunerID, int(xtreamLeaseTTL/time.Second)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) renewXtreamLease(ctx context.Context, tunerID, leaseID string) error {
	tag, err := s.db.Exec(ctx, `UPDATE bloem_livetv_xtream_leases SET expires_at=clock_timestamp()+$3*interval '1 second' WHERE tuner_id=$1 AND lease_id=$2 AND expires_at>clock_timestamp()`, tunerID, leaseID, int(xtreamLeaseTTL/time.Second))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) releaseXtreamLease(ctx context.Context, tunerID, leaseID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM bloem_livetv_xtream_leases WHERE tuner_id=$1 AND lease_id=$2`, tunerID, leaseID)
	return err
}

func (s *PgStore) deleteXtreamTuner(ctx context.Context, id string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback is best effort on returned error
	var found string
	if err = tx.QueryRow(ctx, `SELECT id FROM livetv_tuners WHERE id=$1 AND type='xtream' FOR UPDATE`, id).Scan(&found); err != nil {
		return err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_livetv_xtream_leases WHERE tuner_id=$1 AND expires_at>clock_timestamp())`, id).Scan(&active); err != nil {
		return err
	}
	if active {
		return fmt.Errorf("%w: stop Xtream streams and recordings before removing this provider", ErrLimitExceeded)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM livetv_guide_sources WHERE type='xtream' AND config_json->>'tuner_id'=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM livetv_tuners WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
