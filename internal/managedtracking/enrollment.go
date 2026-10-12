package managedtracking

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/watchsync"
	"github.com/jackc/pgx/v5"
)

var ErrConflict = errors.New("managed_tracking_conflict")

type Enrollment struct {
	Profile                                                                Profile
	ProviderID, ID, DestinationAccountID, DestinationProfileID, Credential string
	Active                                                                 bool
	Generation                                                             uint64
}

func EnrollmentID(instance string, e Enrollment) string {
	b, _ := json.Marshal([]any{instance, e.Profile.AccountID, e.Profile.ProfileID, e.Profile.AccountRevision, e.Profile.Revision, e.DestinationAccountID, e.DestinationProfileID, e.Generation})
	h := sha256.Sum256(b)
	return "enroll_" + hex.EncodeToString(h[:])
}
func positiveID(v string) bool {
	n, err := strconv.ParseInt(v, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == v
}
func (s *Service) EnsureConnection(ctx context.Context, id int, e Enrollment) error {
	if s.Cipher == nil || e.ProviderID != "pastime" || !positiveID(e.Profile.AccountID) || !positiveID(e.DestinationAccountID) || !positiveID(e.DestinationProfileID) || len(e.Credential) > 4096 || e.Active && !strings.HasPrefix(e.Credential, "ptbs_") || !e.Active && e.Credential != "" {
		return ErrAuthority
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = managedWriter(ctx, tx); err != nil {
		return err
	}
	// A shared grant lock holds rotation and revocation until this enrollment
	// commits; they then disable what it wrote. No server-wide lock is taken:
	// events re-check the projection, so a concurrent identity change only
	// leaves work for the next reconcile.
	info, err := authority(ctx, tx, id, grantShare)
	if err != nil {
		return err
	}
	if e.Generation != info.Generation || e.ID != EnrollmentID(info.InstanceID, e) {
		return ErrAuthority
	}
	var p Profile
	err = tx.QueryRow(ctx, `SELECT account_id::text,profile_id,default_profile_id,account_name,name,email,account_revision,revision,account_active,active,identity_issuer,identity_subject FROM bloem_managed_profiles WHERE tenant_id=$1 AND account_id=$2 AND profile_id=$3`, info.Scope, e.Profile.AccountID, e.Profile.ProfileID).Scan(&p.AccountID, &p.ProfileID, &p.DefaultProfileID, &p.AccountName, &p.Name, &p.Email, &p.AccountRevision, &p.Revision, &p.AccountActive, &p.Active, &p.VerifiedIdentityIssuer, &p.VerifiedIdentitySubject)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAuthority
	}
	if err != nil {
		return err
	}
	// The verified identity is part of the profile, so a plugin can neither
	// drop, alter nor inject one.
	if p != e.Profile || e.Active != (p.Active && p.AccountActive) {
		return ErrAuthority
	}
	b, _ := json.Marshal(e)
	digest := sha256.Sum256(b)
	var beforeID string
	var before []byte
	var destAccount, destProfile string
	err = tx.QueryRow(ctx, `SELECT enrollment_id,payload_digest,destination_account_id,destination_profile_id FROM bloem_managed_enrollments WHERE installation_id=$1 AND account_id=$2 AND profile_id=$3 FOR UPDATE`, id, p.AccountID, p.ProfileID).Scan(&beforeID, &before, &destAccount, &destProfile)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	key := watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false)
	var personal bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_capabilities WHERE plugin_installation_id=$1 AND capability_type='watch_sync_provider.v1' AND capability_id='pastime' AND metadata @> '{"managed_personal_state":true}'::jsonb)`, id).Scan(&personal); err != nil {
		return err
	}
	personal = personal && e.Active
	account, _ := strconv.Atoi(p.AccountID)
	if beforeID == e.ID {
		if !bytes.Equal(before, digest[:]) {
			return ErrConflict
		}
		// Replay converges only when the connection still holds exactly this
		// enrollment; a removed or overwritten connection is rewritten below.
		converged, err := s.connectionConverged(ctx, tx, key, account, info.InstanceID, p, e, personal)
		if err != nil {
			return err
		}
		if converged {
			return tx.Commit(ctx)
		}
	} else if beforeID != "" && (destAccount != e.DestinationAccountID || destProfile != e.DestinationProfileID) {
		return ErrConflict
	}
	token, bundle := "", ""
	if e.Active {
		token, err = s.Cipher.Encrypt(e.Credential, watchsync.TokenAAD("access_token", key, account, p.ProfileID))
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(struct {
			AccessToken      string            `json:"access_token"`
			SecretAttributes map[string]string `json:"secret_attributes"`
		}{e.Credential, map[string]string{"instance_id": info.InstanceID, "account_id": p.AccountID, "profile_id": p.ProfileID}})
		bundle, err = s.Cipher.Encrypt(string(raw), watchsync.TokenAAD("plugin_credentials", key, account, p.ProfileID))
		if err != nil {
			return err
		}
	}
	// Archived source profiles have already cascaded their watch connections.
	if p.Active {
		_, err = tx.Exec(ctx, `INSERT INTO watch_provider_connections(provider,user_id,profile_id,provider_account_id,provider_username,access_token,plugin_credentials,import_watched_enabled,import_progress_enabled,export_watched_enabled,scrobble_enabled,export_unwatched_enabled,import_favorites_enabled,export_favorites_enabled,sync_favorite_removals_enabled,import_watchlist_enabled,export_watchlist_enabled,sync_watchlist_removals_enabled,sync_watchlist_order_enabled,import_ratings_enabled,export_ratings_enabled,sync_dropped_enabled)
 VALUES($1,$2,$3,$4,$5,$6,$7,false,false,$8,$8,false,$9,$9,$9,$9,$9,$9,false,$9,$9,false)
 ON CONFLICT(provider,user_id,profile_id) DO UPDATE SET provider_account_id=EXCLUDED.provider_account_id,provider_username=EXCLUDED.provider_username,access_token=EXCLUDED.access_token,plugin_credentials=EXCLUDED.plugin_credentials,refresh_token='',token_expires_at=NULL,import_watched_enabled=false,import_progress_enabled=false,export_watched_enabled=EXCLUDED.export_watched_enabled,export_unwatched_enabled=false,scrobble_enabled=EXCLUDED.scrobble_enabled,import_favorites_enabled=EXCLUDED.import_favorites_enabled,export_favorites_enabled=EXCLUDED.export_favorites_enabled,sync_favorite_removals_enabled=EXCLUDED.sync_favorite_removals_enabled,import_watchlist_enabled=EXCLUDED.import_watchlist_enabled,export_watchlist_enabled=EXCLUDED.export_watchlist_enabled,sync_watchlist_removals_enabled=EXCLUDED.sync_watchlist_removals_enabled,sync_watchlist_order_enabled=false,import_ratings_enabled=EXCLUDED.import_ratings_enabled,export_ratings_enabled=EXCLUDED.export_ratings_enabled,sync_dropped_enabled=false,updated_at=clock_timestamp()`, key, account, p.ProfileID, e.DestinationProfileID, p.Name, token, bundle, e.Active, personal)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM watch_provider_connections WHERE provider=$1 AND user_id=$2 AND profile_id=$3`, key, account, p.ProfileID)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_managed_enrollments(installation_id,account_id,profile_id,enrollment_id,generation,destination_account_id,destination_profile_id,payload_digest,active) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(installation_id,account_id,profile_id) DO UPDATE SET enrollment_id=EXCLUDED.enrollment_id,generation=EXCLUDED.generation,payload_digest=EXCLUDED.payload_digest,active=EXCLUDED.active`, id, account, p.ProfileID, e.ID, info.Generation, e.DestinationAccountID, e.DestinationProfileID, digest[:], e.Active)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// managedWriter marks the transaction as the managed-credential writer that
// bloem_managed_connection_guard lets through.
func managedWriter(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT set_config('bloem.managed_tracking_writer','v1',true)`)
	return err
}

// connectionConverged reports whether the stored connection is exactly what
// enrollment e writes: for an active enrollment, enabled with e's credential
// and attributes; otherwise, holding no credential and nothing enabled.
func (s *Service) connectionConverged(ctx context.Context, tx pgx.Tx, key string, account int, instance string, p Profile, e Enrollment, personal bool) (bool, error) {
	var token, bundle string
	var scrobble, export, settingsMatch bool
	err := tx.QueryRow(ctx, `SELECT access_token,plugin_credentials,scrobble_enabled,export_watched_enabled,
 import_favorites_enabled=$4 AND export_favorites_enabled=$4 AND sync_favorite_removals_enabled=$4 AND import_watchlist_enabled=$4 AND export_watchlist_enabled=$4 AND sync_watchlist_removals_enabled=$4 AND import_ratings_enabled=$4 AND export_ratings_enabled=$4 AND NOT import_watched_enabled AND NOT import_progress_enabled AND NOT export_unwatched_enabled AND NOT sync_watchlist_order_enabled AND NOT sync_dropped_enabled
 FROM watch_provider_connections WHERE provider=$1 AND user_id=$2 AND profile_id=$3`, key, account, p.ProfileID, personal).Scan(&token, &bundle, &scrobble, &export, &settingsMatch)
	if errors.Is(err, pgx.ErrNoRows) {
		return !e.Active, nil
	}
	if err != nil {
		return false, err
	}
	if !settingsMatch {
		return false, nil
	}
	if !e.Active {
		// A suspended account keeps its active profile's row, emptied.
		return p.Active && token == "" && bundle == "" && !scrobble && !export, nil
	}
	if !scrobble || !export {
		return false, nil
	}
	// An undecryptable value is not an error here: it is a connection that
	// no longer holds this enrollment, which the caller rewrites.
	if plain, ok := s.open(token, watchsync.TokenAAD("access_token", key, account, p.ProfileID)); !ok || plain != e.Credential {
		return false, nil
	}
	raw, ok := s.open(bundle, watchsync.TokenAAD("plugin_credentials", key, account, p.ProfileID))
	if !ok {
		return false, nil
	}
	stored, ok := decodeCredentials(raw)
	return ok && stored.AccessToken == e.Credential && stored.SecretAttributes["account_id"] == p.AccountID && stored.SecretAttributes["profile_id"] == p.ProfileID && stored.SecretAttributes["instance_id"] == instance, nil
}

type credentialBundle struct {
	AccessToken      string            `json:"access_token"`
	SecretAttributes map[string]string `json:"secret_attributes"`
}

func decodeCredentials(raw string) (credentialBundle, bool) {
	var b credentialBundle
	err := json.Unmarshal([]byte(raw), &b)
	return b, err == nil
}

// open decrypts a stored value, reporting only whether it opened.
func (s *Service) open(ciphertext, aad string) (string, bool) {
	plain, err := s.Cipher.Decrypt(ciphertext, aad)
	return plain, err == nil
}

func disableConnections(ctx context.Context, tx pgx.Tx, id int) error {
	if err := managedWriter(ctx, tx); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE watch_provider_connections SET access_token='',refresh_token='',plugin_credentials='',scrobble_enabled=false,export_watched_enabled=false,import_favorites_enabled=false,export_favorites_enabled=false,sync_favorite_removals_enabled=false,import_watchlist_enabled=false,export_watchlist_enabled=false,sync_watchlist_removals_enabled=false,import_ratings_enabled=false,export_ratings_enabled=false,updated_at=clock_timestamp() WHERE provider=$1`, watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false))
	return err
}
func (s *Service) Revoke(ctx context.Context, id int) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE bloem_managed_grants SET active=false,generation=generation+1,min_generation=generation+1,renewed_at=clock_timestamp() WHERE installation_id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAuthority
	}
	if err = disableConnections(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Rotate is backend-only. Existing profile credentials are disabled until reconciled.
func (s *Service) Rotate(ctx context.Context, id int) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = authority(ctx, tx, id, grantUpdate); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_managed_grants SET generation=generation+1,min_generation=generation+1,renewed_at=clock_timestamp(),acked_revision=0 WHERE installation_id=$1`, id); err != nil {
		return err
	}
	if err = disableConnections(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
