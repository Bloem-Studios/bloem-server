package notifications

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	"github.com/Silo-Server/silo-server/internal/secret"
)

// adminPushLease is how long a claimed attempt stays out of other workers'
// reach. A worker that dies mid-send leaves the attempt to be claimed again.
const adminPushLease = 2 * time.Minute

// adminDisplayTokenPrefix marks the token a phone uses to read its own
// notification text. It opens one route (the content read) and the device's own
// retire call, and nothing else.
const adminDisplayTokenPrefix = "bad_"

// AdminPushDevice is one admin-app installation registered for push.
type AdminPushDevice struct {
	ID              string
	UserID          int
	InstallationID  string
	Platform        string
	APNsEnvironment string
	APNsTopic       string
	TokenCiphertext string
	TokenHash       string
	ServerDeviceID  string
	DisabledKinds   []string
	LoginSessionID  string
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// kindEnabled applies the device's own switches.
func (d AdminPushDevice) kindEnabled(kind string) bool {
	for _, off := range d.DisabledKinds {
		if off == kind {
			return false
		}
	}
	return true
}

// adminDeliveryRow is a stored notification.
type adminDeliveryRow struct {
	ID          string
	UserID      int
	Kind        string
	CollapseKey string
	SubjectType string
	SubjectID   string
	Title       string
	Body        string
	LockText    string
	Badge       *int
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// adminPushAttempt is one pending send of a delivery to a device.
type adminPushAttempt struct {
	ID            string
	DeliveryID    string
	DeviceID      string
	AttemptNumber int
}

type adminPushStore struct{ pool *pgxpool.Pool }

func newAdminPushStore(pool *pgxpool.Pool) *adminPushStore { return &adminPushStore{pool: pool} }

const adminDeviceColumns = `d.id, d.user_id, d.installation_id, d.platform, COALESCE(d.apns_environment, ''), COALESCE(d.apns_topic, ''),
	d.token_ciphertext, d.token_hash, d.server_device_id, d.disabled_kinds, COALESCE(d.login_session_id, ''), d.enabled, d.created_at, d.updated_at`

// adminLoginLive is true while the login that registered the device is still
// valid, or when the registration has no known login.
const adminLoginLive = `(d.login_session_id IS NULL OR EXISTS (
	SELECT 1 FROM auth_sessions s WHERE s.id = d.login_session_id AND ` + authSessionLive + `))`

// adminAccountLive is the admin-only rule: an enabled account with the admin role.
const adminAccountLive = `EXISTS (SELECT 1 FROM users u WHERE u.id = d.user_id AND u.enabled AND u.role = 'admin')`

func scanAdminDevice(row pgx.Row) (*AdminPushDevice, error) {
	var d AdminPushDevice
	if err := row.Scan(&d.ID, &d.UserID, &d.InstallationID, &d.Platform, &d.APNsEnvironment, &d.APNsTopic,
		&d.TokenCiphertext, &d.TokenHash, &d.ServerDeviceID, &d.DisabledKinds, &d.LoginSessionID, &d.Enabled,
		&d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	return &d, nil
}

func newAdminDisplayToken() (token, hash string, err error) {
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	token = adminDisplayTokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
	return token, adminDisplayTokenHash(token), nil
}

func adminDisplayTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func adminPushTokenAAD(id string) string {
	return secret.RowAAD("bloem_admin_push_devices", "token", id)
}

// adminPushUpsert is a registration after validation.
type adminPushUpsert struct {
	UserID          int
	InstallationID  string
	Platform        string
	APNsEnvironment string
	APNsTopic       string
	Token           string
	TokenHash       string
	LoginSessionID  string
	// DisabledKinds replaces the device's switches when set; nil keeps them.
	DisabledKinds *[]string
}

// upsertDevice registers or refreshes a device and mints a new display token.
// A token that another installation or account held moves to this one, so a
// phone that signs in as someone else stops receiving the old account's pushes.
func (s *adminPushStore) upsertDevice(ctx context.Context, cipher *secret.Cipher, in adminPushUpsert) (*AdminPushDevice, string, error) {
	displayToken, displayHash, err := newAdminDisplayToken()
	if err != nil {
		return nil, "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err = tx.Exec(ctx, `DELETE FROM bloem_admin_push_devices
		WHERE platform = $1 AND token_hash = $2 AND NOT (user_id = $3 AND installation_id = $4)`,
		in.Platform, in.TokenHash, in.UserID, in.InstallationID); err != nil {
		return nil, "", fmt.Errorf("release token: %w", err)
	}

	var existingID string
	err = tx.QueryRow(ctx, `SELECT id FROM bloem_admin_push_devices
		WHERE user_id = $1 AND installation_id = $2 AND platform = $3 FOR UPDATE`,
		in.UserID, in.InstallationID, in.Platform).Scan(&existingID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, "", err
	}

	var device *AdminPushDevice
	if errors.Is(err, pgx.ErrNoRows) {
		device, err = insertAdminDevice(ctx, tx, cipher, in, displayHash)
	} else {
		device, err = updateAdminDevice(ctx, tx, cipher, in, existingID, displayHash)
	}
	if err != nil {
		return nil, "", fmt.Errorf("write device: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return device, displayToken, nil
}

func insertAdminDevice(ctx context.Context, tx pgx.Tx, cipher *secret.Cipher, in adminPushUpsert, displayHash string) (*AdminPushDevice, error) {
	id := ulid.Make().String()
	ciphertext, err := cipher.Encrypt(in.Token, adminPushTokenAAD(id))
	if err != nil {
		return nil, fmt.Errorf("encrypt token: %w", err)
	}
	disabled := []string{}
	if in.DisabledKinds != nil {
		disabled = *in.DisabledKinds
	}
	return scanAdminDevice(tx.QueryRow(ctx, `
		INSERT INTO bloem_admin_push_devices AS d (id, user_id, installation_id, platform, apns_environment, apns_topic,
			token_ciphertext, token_hash, server_device_id, display_token_hash, disabled_kinds, login_session_id)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7, $8, $9, $10, $11, NULLIF($12, ''))
		RETURNING `+adminDeviceColumns,
		id, in.UserID, in.InstallationID, in.Platform, in.APNsEnvironment, in.APNsTopic,
		ciphertext, in.TokenHash, ulid.Make().String(), displayHash, disabled, in.LoginSessionID))
}

func updateAdminDevice(ctx context.Context, tx pgx.Tx, cipher *secret.Cipher, in adminPushUpsert, id, displayHash string) (*AdminPushDevice, error) {
	ciphertext, err := cipher.Encrypt(in.Token, adminPushTokenAAD(id))
	if err != nil {
		return nil, fmt.Errorf("encrypt token: %w", err)
	}
	return scanAdminDevice(tx.QueryRow(ctx, `
		UPDATE bloem_admin_push_devices AS d SET
			apns_environment = NULLIF($2, ''), apns_topic = NULLIF($3, ''),
			token_ciphertext = $4, token_hash = $5, display_token_hash = $6,
			disabled_kinds = COALESCE($7, d.disabled_kinds), login_session_id = NULLIF($8, ''),
			enabled = true, last_failure_at = NULL, last_failure_code = NULL, updated_at = now()
		WHERE d.id = $1
		RETURNING `+adminDeviceColumns,
		id, in.APNsEnvironment, in.APNsTopic, ciphertext, in.TokenHash, displayHash,
		in.DisabledKinds, in.LoginSessionID))
}

// setKinds applies per-kind switches (true = on) to a device the account owns.
func (s *adminPushStore) setKinds(ctx context.Context, userID int, deviceID string, kinds map[string]bool) (*AdminPushDevice, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	device, err := scanAdminDevice(tx.QueryRow(ctx,
		`SELECT `+adminDeviceColumns+` FROM bloem_admin_push_devices d WHERE d.server_device_id = $1 AND d.user_id = $2 FOR UPDATE`,
		deviceID, userID))
	if err != nil {
		return nil, err
	}
	off := map[string]bool{}
	for _, kind := range device.DisabledKinds {
		off[kind] = true
	}
	for kind, on := range kinds {
		off[kind] = !on
	}
	disabled := []string{}
	for _, kind := range adminPushKinds {
		if off[kind.ID] {
			disabled = append(disabled, kind.ID)
		}
	}
	device, err = scanAdminDevice(tx.QueryRow(ctx, `UPDATE bloem_admin_push_devices AS d SET disabled_kinds = $2, updated_at = now()
		WHERE d.id = $1 RETURNING `+adminDeviceColumns, device.ID, disabled))
	if err != nil {
		return nil, err
	}
	return device, tx.Commit(ctx)
}

// deleteDevice removes a device the account owns.
func (s *adminPushStore) deleteDevice(ctx context.Context, userID int, serverDeviceID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM bloem_admin_push_devices WHERE server_device_id = $1 AND user_id = $2`, serverDeviceID, userID)
	return tag.RowsAffected() > 0, err
}

// retireByDisplayToken removes the device the token belongs to; it works after
// the login that registered the device has ended.
func (s *adminPushStore) retireByDisplayToken(ctx context.Context, tokenHash, serverDeviceID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM bloem_admin_push_devices WHERE display_token_hash = $1 AND server_device_id = $2`, tokenHash, serverDeviceID)
	return tag.RowsAffected() > 0, err
}

// deviceByDisplayToken finds the device a display token was issued to, and
// whether its account is still an enabled admin.
func (s *adminPushStore) deviceByDisplayToken(ctx context.Context, tokenHash string) (*AdminPushDevice, bool, error) {
	var admin bool
	row := s.pool.QueryRow(ctx, `SELECT `+adminDeviceColumns+`, `+adminAccountLive+` FROM bloem_admin_push_devices d WHERE d.display_token_hash = $1`, tokenHash)
	var d AdminPushDevice
	err := row.Scan(&d.ID, &d.UserID, &d.InstallationID, &d.Platform, &d.APNsEnvironment, &d.APNsTopic,
		&d.TokenCiphertext, &d.TokenHash, &d.ServerDeviceID, &d.DisabledKinds, &d.LoginSessionID, &d.Enabled,
		&d.CreatedAt, &d.UpdatedAt, &admin)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &d, admin, nil
}

// deviceByID loads a device and whether it may still be sent to: the account
// is an enabled admin and the registering login is live.
func (s *adminPushStore) deviceByID(ctx context.Context, id string) (*AdminPushDevice, bool, error) {
	var d AdminPushDevice
	var live bool
	err := s.pool.QueryRow(ctx, `SELECT `+adminDeviceColumns+`, (`+adminAccountLive+` AND `+adminLoginLive+`) FROM bloem_admin_push_devices d WHERE d.id = $1`, id).
		Scan(&d.ID, &d.UserID, &d.InstallationID, &d.Platform, &d.APNsEnvironment, &d.APNsTopic,
			&d.TokenCiphertext, &d.TokenHash, &d.ServerDeviceID, &d.DisabledKinds, &d.LoginSessionID, &d.Enabled,
			&d.CreatedAt, &d.UpdatedAt, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &d, live, nil
}

// isAdminAccount reports whether the account is currently an enabled admin.
func (s *adminPushStore) isAdminAccount(ctx context.Context, userID int) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(enabled, false) AND role = 'admin' FROM users WHERE id = $1`, userID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return ok, err
}

// adminAccountIDs resolves an audience to enabled admin accounts.
func (s *adminPushStore) adminAccountIDs(ctx context.Context, audience AdminPushAudience, requesterUserID int) ([]int, error) {
	query := `SELECT u.id FROM users u WHERE u.enabled AND u.role = 'admin' ORDER BY u.id`
	args := []any{}
	if audience == AudienceRequestAdmins && requesterUserID > 0 {
		// Administrator authority is bounded to the requester's organization
		// (requests.SetTenantScopeResolver), so only those admins can act.
		query = `SELECT u.id FROM users u WHERE u.enabled AND u.role = 'admin' AND (
			NOT EXISTS (SELECT 1 FROM organization_memberships r WHERE r.account_id = $1 AND r.status = 'active')
			OR EXISTS (SELECT 1 FROM organization_memberships a JOIN organization_memberships r
				ON r.organization_id = a.organization_id AND r.account_id = $1 AND r.status = 'active'
				WHERE a.account_id = u.id AND a.status = 'active')
		) ORDER BY u.id`
		args = append(args, requesterUserID)
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// recipientDevices lists the enabled devices of userID that should get kind:
// the account is still an admin, the login is live, and the device has not
// switched the kind off.
func (s *adminPushStore) recipientDevices(ctx context.Context, userID int, kind string) ([]AdminPushDevice, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+adminDeviceColumns+` FROM bloem_admin_push_devices d
		WHERE d.user_id = $1 AND d.enabled AND NOT ($2 = ANY(d.disabled_kinds)) AND `+adminAccountLive+` AND `+adminLoginLive+`
		ORDER BY d.id`, userID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminPushDevice
	for rows.Next() {
		d, err := scanAdminDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// insertDelivery stores the delivery and one pending attempt per device in one
// transaction. It reports false, and writes nothing, when the delivery id
// already exists: another replica or an earlier pass handled the same event.
func (s *adminPushStore) insertDelivery(ctx context.Context, row adminDeliveryRow, devices []AdminPushDevice) ([]adminPushAttempt, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `INSERT INTO bloem_admin_push_deliveries
		(id, user_id, kind, collapse_key, subject_type, subject_id, title, body, lock_text, badge, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT (id) DO NOTHING`,
		row.ID, row.UserID, row.Kind, row.CollapseKey, row.SubjectType, row.SubjectID, row.Title, row.Body, row.LockText, row.Badge, row.ExpiresAt)
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 0 {
		return nil, false, nil
	}
	attempts := make([]adminPushAttempt, 0, len(devices))
	for _, device := range devices {
		attempt := adminPushAttempt{ID: ulid.Make().String(), DeliveryID: row.ID, DeviceID: device.ID}
		if _, err = tx.Exec(ctx, `INSERT INTO bloem_admin_push_attempts (id, delivery_id, device_id) VALUES ($1, $2, $3)
			ON CONFLICT (delivery_id, device_id) DO NOTHING`, attempt.ID, attempt.DeliveryID, attempt.DeviceID); err != nil {
			return nil, false, err
		}
		attempts = append(attempts, attempt)
	}
	return attempts, true, tx.Commit(ctx)
}

// claimDue leases the attempts that are ready to send or retry.
func (s *adminPushStore) claimDue(ctx context.Context, limit int) ([]adminPushAttempt, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE bloem_admin_push_attempts SET outcome = 'retrying', next_retry_at = now() + $2, updated_at = now()
		WHERE id IN (
			SELECT id FROM bloem_admin_push_attempts
			WHERE outcome IN ('pending', 'retrying') AND (next_retry_at IS NULL OR next_retry_at <= now())
			ORDER BY created_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING id, delivery_id, device_id, attempt_number`, limit, adminPushLease)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []adminPushAttempt
	for rows.Next() {
		var a adminPushAttempt
		if err := rows.Scan(&a.ID, &a.DeliveryID, &a.DeviceID, &a.AttemptNumber); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *adminPushStore) delivery(ctx context.Context, id string) (*adminDeliveryRow, error) {
	var r adminDeliveryRow
	err := s.pool.QueryRow(ctx, `SELECT id, user_id, kind, collapse_key, subject_type, subject_id, title, body, lock_text, badge, created_at, expires_at
		FROM bloem_admin_push_deliveries WHERE id = $1`, id).
		Scan(&r.ID, &r.UserID, &r.Kind, &r.CollapseKey, &r.SubjectType, &r.SubjectID, &r.Title, &r.Body, &r.LockText, &r.Badge, &r.CreatedAt, &r.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}

// deliveryForDevice is the content read: a delivery is visible only to the
// device it was sent to, for the account it was written for, until it expires.
func (s *adminPushStore) deliveryForDevice(ctx context.Context, deviceID string, userID int, deliveryID string) (*adminDeliveryRow, error) {
	var r adminDeliveryRow
	err := s.pool.QueryRow(ctx, `SELECT dl.id, dl.user_id, dl.kind, dl.collapse_key, dl.subject_type, dl.subject_id, dl.title, dl.body, dl.lock_text, dl.badge, dl.created_at, dl.expires_at
		FROM bloem_admin_push_deliveries dl
		JOIN bloem_admin_push_attempts a ON a.delivery_id = dl.id
		WHERE dl.id = $1 AND a.device_id = $2 AND dl.user_id = $3 AND dl.expires_at > now()`, deliveryID, deviceID, userID).
		Scan(&r.ID, &r.UserID, &r.Kind, &r.CollapseKey, &r.SubjectType, &r.SubjectID, &r.Title, &r.Body, &r.LockText, &r.Badge, &r.CreatedAt, &r.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}

func (s *adminPushStore) finalize(ctx context.Context, attemptID, outcome string, number int, relayRequestID string, status *int, reason, message string, next *time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE bloem_admin_push_attempts SET outcome = $2, attempt_number = $3,
		relay_request_id = NULLIF($4, ''), upstream_status = $5, upstream_reason = NULLIF($6, ''), failure_message = NULLIF($7, ''),
		next_retry_at = $8, updated_at = now() WHERE id = $1`, attemptID, outcome, number, relayRequestID, status, reason, message, next)
	return err
}

func (s *adminPushStore) recordSuccess(ctx context.Context, deviceID string) {
	_, _ = s.pool.Exec(ctx, `UPDATE bloem_admin_push_devices SET last_success_at = now(), last_failure_at = NULL, last_failure_code = NULL WHERE id = $1`, deviceID)
}

// recordFailure notes a failed send; a token the push service says is gone
// switches the device off until the app registers again.
func (s *adminPushStore) recordFailure(ctx context.Context, deviceID, code string, terminal bool) {
	_, _ = s.pool.Exec(ctx, `UPDATE bloem_admin_push_devices SET last_failure_at = now(), last_failure_code = $2,
		enabled = enabled AND NOT $3 WHERE id = $1`, deviceID, code, terminal)
}

// purgeExpired drops deliveries (and their attempts) a day past their expiry.
func (s *adminPushStore) purgeExpired(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM bloem_admin_push_deliveries WHERE expires_at < now() - interval '1 day'`)
	return tag.RowsAffected(), err
}
