package compatapp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store owns the SQL for compat application trust state. Every method takes
// only digests, never raw secrets.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		return nil
	}
	return &Store{pool: pool}
}

func (st *Store) Begin(ctx context.Context) (pgx.Tx, error) {
	return st.pool.Begin(ctx)
}

// enrollmentRow is the persisted state of one enrollment secret.
type enrollmentRow struct {
	ID           string
	Kind         Kind
	SecretDigest string
	Capabilities []string
	ExpiresAt    time.Time
	ConsumedAt   *time.Time
}

// credentialRow is the persisted state of one service credential.
type credentialRow struct {
	ID           string
	SecretDigest string
	ExpiresAt    time.Time
	RevokedAt    *time.Time
}

// applicationRow is the persisted trust state of one application.
type applicationRow struct {
	ID             string
	Kind           Kind
	InstanceID     string
	Capabilities   []string
	TLSFingerprint *string
	Enabled        bool
	RevokedAt      *time.Time
	APIRangeMin    int
	APIRangeMax    int
	Revision       int64
}

func (st *Store) InsertEnrollment(ctx context.Context, q queryExecer, id, kind, secretDigest string, capabilities []string, expiresAt time.Time) error {
	if _, err := q.Exec(ctx, `
		INSERT INTO compat_application_enrollments (id, kind, secret_digest, granted_capabilities, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		id, kind, secretDigest, capabilities, expiresAt); err != nil {
		return fmt.Errorf("insert enrollment: %w", sanitizeDatabaseError(err))
	}
	return nil
}

// LockEnrollmentByDigest loads an enrollment row and takes its row lock, so
// concurrent redemptions of the same secret serialize and exactly one can
// consume it.
func (st *Store) LockEnrollmentByDigest(ctx context.Context, tx pgx.Tx, secretDigest string) (enrollmentRow, error) {
	var row enrollmentRow
	err := tx.QueryRow(ctx, `
		SELECT id, kind, secret_digest, granted_capabilities, expires_at, consumed_at
		FROM compat_application_enrollments
		WHERE secret_digest = $1
		FOR UPDATE`, secretDigest).Scan(
		&row.ID, &row.Kind, &row.SecretDigest, &row.Capabilities, &row.ExpiresAt, &row.ConsumedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return enrollmentRow{}, ErrEnrollmentDenied
		}
		return enrollmentRow{}, fmt.Errorf("lock enrollment: %w", sanitizeDatabaseError(err))
	}
	return row, nil
}

func (st *Store) ConsumeEnrollment(ctx context.Context, tx pgx.Tx, enrollmentID, applicationID string, at time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE compat_application_enrollments
		SET consumed_at = $2, application_id = $3
		WHERE id = $1 AND consumed_at IS NULL`,
		enrollmentID, at, applicationID)
	if err != nil {
		return fmt.Errorf("consume enrollment: %w", sanitizeDatabaseError(err))
	}
	if tag.RowsAffected() != 1 {
		return ErrEnrollmentDenied
	}
	return nil
}

func (st *Store) InsertApplication(
	ctx context.Context,
	tx pgx.Tx,
	kind, instanceID, version, imageDigest string,
	apiRangeMin, apiRangeMax int,
	capabilities []string,
	tlsFingerprint string,
	at time.Time,
) (string, error) {
	var digest *string
	if imageDigest != "" {
		digest = &imageDigest
	}
	var fingerprint *string
	if tlsFingerprint != "" {
		fingerprint = &tlsFingerprint
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO compat_applications (
			kind, instance_id, version, image_digest,
			api_range_min, api_range_max, granted_capabilities,
			tls_fingerprint, last_contact_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $9)
		RETURNING id::text`,
		kind, instanceID, version, digest,
		apiRangeMin, apiRangeMax, capabilities,
		fingerprint, at).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", ErrInstanceAlreadyEnrolled
		}
		return "", fmt.Errorf("insert application: %w", sanitizeDatabaseError(err))
	}
	return id, nil
}

func (st *Store) InsertCredential(ctx context.Context, tx pgx.Tx, id, applicationID, secretDigest string, expiresAt time.Time) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO compat_application_credentials (id, application_id, secret_digest, expires_at)
		VALUES ($1, $2, $3, $4)`,
		id, applicationID, secretDigest, expiresAt); err != nil {
		return fmt.Errorf("insert credential: %w", sanitizeDatabaseError(err))
	}
	return nil
}

// CredentialByDigest resolves a presented credential digest to its credential
// and application trust state in one read.
func (st *Store) CredentialByDigest(ctx context.Context, secretDigest string) (credentialRow, applicationRow, error) {
	var credential credentialRow
	var application applicationRow
	err := st.pool.QueryRow(ctx, `
		SELECT credentials.id::text, credentials.secret_digest, credentials.expires_at, credentials.revoked_at,
		       applications.id::text, applications.kind, applications.instance_id,
		       applications.granted_capabilities, applications.tls_fingerprint,
		       applications.enabled, applications.revoked_at,
		       applications.api_range_min, applications.api_range_max
		FROM compat_application_credentials credentials
		JOIN compat_applications applications ON applications.id = credentials.application_id
		WHERE credentials.secret_digest = $1`, secretDigest).Scan(
		&credential.ID, &credential.SecretDigest, &credential.ExpiresAt, &credential.RevokedAt,
		&application.ID, &application.Kind, &application.InstanceID,
		&application.Capabilities, &application.TLSFingerprint,
		&application.Enabled, &application.RevokedAt,
		&application.APIRangeMin, &application.APIRangeMax)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return credentialRow{}, applicationRow{}, ErrCredentialInvalid
		}
		return credentialRow{}, applicationRow{}, fmt.Errorf("load credential: %w", sanitizeDatabaseError(err))
	}
	return credential, application, nil
}

// RenewCredential slides the credential expiry forward and records companion
// contact in one statement.
func (st *Store) RenewCredential(ctx context.Context, credentialID string, expiresAt time.Time, applicationID string, at time.Time) error {
	if _, err := st.pool.Exec(ctx, `
		WITH renewed AS (
			UPDATE compat_application_credentials SET expires_at = $2 WHERE id = $1
		)
		UPDATE compat_applications SET last_contact_at = $4 WHERE id = $3`,
		credentialID, expiresAt, applicationID, at); err != nil {
		return fmt.Errorf("renew credential: %w", sanitizeDatabaseError(err))
	}
	return nil
}

// lockApplicationColumns is the trust state every lifecycle path decides on.
const lockApplicationColumns = `id::text, kind, instance_id, granted_capabilities, tls_fingerprint,
		       enabled, revoked_at, api_range_min, api_range_max, revision`

// LockApplication loads an application's trust state under its row lock.
func (st *Store) LockApplication(ctx context.Context, tx pgx.Tx, applicationID string) (applicationRow, error) {
	return scanLockedApplication(tx.QueryRow(ctx, `
		SELECT `+lockApplicationColumns+`
		FROM compat_applications
		WHERE id = $1
		FOR UPDATE`, applicationID))
}

// LockApplicationByInstance is the administration surface's addressing mode:
// it names an application by the instance identity the operator sees, not by
// the internal row id. instance_id is unique across kinds (see
// migrations/sql/20260813200400_compat_application_revision.sql), so the
// address resolves to at most one row.
//
// The row lock is the whole concurrency model: two administrators deciding
// from the same page serialize here, and the second one re-reads the row the
// first one committed, so its revision guard sees the moved revision instead
// of the one it read before the race.
func (st *Store) LockApplicationByInstance(ctx context.Context, tx pgx.Tx, instanceID string) (applicationRow, error) {
	return scanLockedApplication(tx.QueryRow(ctx, `
		SELECT `+lockApplicationColumns+`
		FROM compat_applications
		WHERE instance_id = $1
		FOR UPDATE`, instanceID))
}

func scanLockedApplication(row pgx.Row) (applicationRow, error) {
	var application applicationRow
	err := row.Scan(
		&application.ID, &application.Kind, &application.InstanceID, &application.Capabilities,
		&application.TLSFingerprint, &application.Enabled, &application.RevokedAt,
		&application.APIRangeMin, &application.APIRangeMax, &application.Revision)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return applicationRow{}, ErrApplicationNotFound
		}
		return applicationRow{}, fmt.Errorf("lock application: %w", sanitizeDatabaseError(err))
	}
	return application, nil
}

func (st *Store) RevokeCredentials(ctx context.Context, tx pgx.Tx, applicationID string, at time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE compat_application_credentials
		SET revoked_at = $2
		WHERE application_id = $1 AND revoked_at IS NULL`,
		applicationID, at); err != nil {
		return fmt.Errorf("revoke credentials: %w", sanitizeDatabaseError(err))
	}
	return nil
}

func (st *Store) MarkApplicationRevoked(ctx context.Context, tx pgx.Tx, applicationID string, at time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE compat_applications SET revoked_at = $2 WHERE id = $1`,
		applicationID, at); err != nil {
		return fmt.Errorf("mark application revoked: %w", sanitizeDatabaseError(err))
	}
	return nil
}

func (st *Store) SetApplicationEnabled(ctx context.Context, tx pgx.Tx, applicationID string, enabled bool) error {
	if _, err := tx.Exec(ctx, `
		UPDATE compat_applications SET enabled = $2 WHERE id = $1`,
		applicationID, enabled); err != nil {
		return fmt.Errorf("set application enabled: %w", sanitizeDatabaseError(err))
	}
	return nil
}

// MarkCredentialRotated records an administrator-forced rotation. It is a
// governed column, so writing it is what advances the application's revision;
// companion self-renewal deliberately does not call it.
func (st *Store) MarkCredentialRotated(ctx context.Context, tx pgx.Tx, applicationID string, at time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE compat_applications SET credential_rotated_at = $2 WHERE id = $1`,
		applicationID, at); err != nil {
		return fmt.Errorf("mark credential rotated: %w", sanitizeDatabaseError(err))
	}
	return nil
}

func (st *Store) RecordHeartbeat(ctx context.Context, tx pgx.Tx, applicationID string, status HealthStatus, at time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE compat_applications SET health_status = $2, last_contact_at = $3 WHERE id = $1`,
		applicationID, string(status), at); err != nil {
		return fmt.Errorf("record heartbeat: %w", sanitizeDatabaseError(err))
	}
	return nil
}

// InsertAudit records one trust-lifecycle event. detail must never contain a
// secret or digest; it is operator-facing context only.
func (st *Store) InsertAudit(ctx context.Context, q queryExecer, applicationID, enrollmentID, event string, detail map[string]any) error {
	var appID, enrollID *string
	if applicationID != "" {
		appID = &applicationID
	}
	if enrollmentID != "" {
		enrollID = &enrollmentID
	}
	if detail == nil {
		detail = map[string]any{}
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO compat_application_audit (application_id, enrollment_id, event, detail)
		VALUES ($1, $2, $3, $4)`,
		appID, enrollID, event, detail); err != nil {
		return fmt.Errorf("insert audit event: %w", sanitizeDatabaseError(err))
	}
	return nil
}

// queryExecer lets audit rows ride either a transaction or the pool.
type queryExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// rowQuerier lets a single-row read ride either a transaction or the pool.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// applicationViewColumns is the administration view. It names columns
// explicitly and names no credential table, so the view cannot grow a secret
// by accident.
const applicationViewColumns = `id::text, kind, instance_id, version, image_digest,
		       api_range_min, api_range_max, granted_capabilities,
		       tls_fingerprint, enabled, health_status,
		       last_contact_at, revoked_at, credential_rotated_at,
		       created_at, updated_at, revision`

// Application loads the administration view of one application.
func (st *Store) Application(ctx context.Context, applicationID string) (Application, error) {
	return st.ApplicationVia(ctx, st.pool, applicationID)
}

// ApplicationVia is Application against a caller-supplied querier, so a
// mutation can read back the view it just wrote inside its own transaction.
// Reading after commit would report a revision some other writer had already
// moved on from.
func (st *Store) ApplicationVia(ctx context.Context, q rowQuerier, applicationID string) (Application, error) {
	app, err := scanApplication(q.QueryRow(ctx, `
		SELECT `+applicationViewColumns+`
		FROM compat_applications
		WHERE id = $1`, applicationID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Application{}, ErrApplicationNotFound
		}
		return Application{}, fmt.Errorf("load application: %w", sanitizeDatabaseError(err))
	}
	return app, nil
}

// ListApplications returns the administration view of every enrolled
// application, ordered so two reads of an unchanged deployment agree.
func (st *Store) ListApplications(ctx context.Context) ([]Application, error) {
	rows, err := st.pool.Query(ctx, `
		SELECT `+applicationViewColumns+`
		FROM compat_applications
		ORDER BY kind, instance_id`)
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", sanitizeDatabaseError(err))
	}
	defer rows.Close()
	applications := make([]Application, 0)
	for rows.Next() {
		app, scanErr := scanApplication(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan application: %w", sanitizeDatabaseError(scanErr))
		}
		applications = append(applications, app)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read applications: %w", sanitizeDatabaseError(err))
	}
	return applications, nil
}

// scanRow is the shared shape of pgx.Row and pgx.Rows for a single record.
type scanRow interface {
	Scan(dest ...any) error
}

func scanApplication(row scanRow) (Application, error) {
	var app Application
	var imageDigest, fingerprint *string
	var capabilities []string
	var health string
	if err := row.Scan(
		&app.ID, &app.Kind, &app.InstanceID, &app.Version, &imageDigest,
		&app.APIRangeMin, &app.APIRangeMax, &capabilities,
		&fingerprint, &app.Enabled, &health,
		&app.LastContactAt, &app.RevokedAt, &app.CredentialRotatedAt,
		&app.CreatedAt, &app.UpdatedAt, &app.Revision); err != nil {
		return Application{}, err
	}
	if imageDigest != nil {
		app.ImageDigest = *imageDigest
	}
	if fingerprint != nil {
		app.TLSFingerprint = *fingerprint
	}
	app.Health = HealthStatus(health)
	app.Capabilities = toCapabilities(capabilities)
	return app, nil
}

// AuditEvents returns an application's trust-lifecycle records in insertion
// order.
func (st *Store) AuditEvents(ctx context.Context, applicationID string) ([]AuditEvent, error) {
	rows, err := st.pool.Query(ctx, `
		SELECT id, application_id::text, enrollment_id::text, event, created_at
		FROM compat_application_audit
		WHERE application_id = $1
		ORDER BY id`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("load audit events: %w", sanitizeDatabaseError(err))
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var appID, enrollID *string
		if err := rows.Scan(&event.ID, &appID, &enrollID, &event.Event, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		if appID != nil {
			event.ApplicationID = *appID
		}
		if enrollID != nil {
			event.EnrollmentID = *enrollID
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read audit events: %w", sanitizeDatabaseError(err))
	}
	return events, nil
}

func toCapabilities(values []string) []Capability {
	capabilities := make([]Capability, 0, len(values))
	for _, value := range values {
		capabilities = append(capabilities, Capability(value))
	}
	return capabilities
}

// sanitizeDatabaseError reduces a driver error to its code and message so a
// constraint detail echoing row values cannot travel into logs or responses.
func sanitizeDatabaseError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("%s (SQLSTATE %s)", pgErr.Message, pgErr.Code)
	}
	return err
}
