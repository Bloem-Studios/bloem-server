package preview

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

const SharedUsername = "bloem-preview"

var ErrForbidden = errors.New("preview: platform administrator required")

// Admin owns the preview control plane; every operation rechecks durable
// platform authority, and writes serialize with guest exchange and shutdown.
type Admin struct {
	pool        *pgxpool.Pool
	serverID    ServerID
	store       *Store
	servedHosts []string
}

func NewAdmin(pool *pgxpool.Pool, serverID ServerID, servedHosts ...string) *Admin {
	return &Admin{pool: pool, serverID: serverID, store: NewStore(pool), servedHosts: servedHosts}
}

func (a *Admin) begin(ctx context.Context, actor int, issuer string) (pgx.Tx, Settings, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, Settings{}, err
	}
	fail := func(err error) (pgx.Tx, Settings, error) { _ = tx.Rollback(ctx); return nil, Settings{}, err }
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, settingsLockKey); err != nil {
		return fail(err)
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT enabled AND role='admin' FROM users WHERE id=$1 FOR SHARE`, actor).Scan(&eligible)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !eligible {
		return fail(ErrForbidden)
	}
	if err != nil {
		return fail(err)
	}
	s, err := getSettings(ctx, tx)
	if err != nil {
		return fail(err)
	}
	if issuer != "" && (strings.TrimSpace(issuer) != issuer || len(issuer) > 128 || s.Issuer != "" && s.Issuer != issuer) {
		return fail(fmt.Errorf("%w: issuer ownership mismatch", ErrInvalidSettings))
	}
	return tx, s, nil
}

func (a *Admin) Settings(ctx context.Context, actor int) (Settings, error) {
	tx, s, err := a.begin(ctx, actor, "")
	if err != nil {
		return Settings{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return s, nil
}

// EnsureAccount never adopts a pre-existing account with the reserved name.
// Account, primary profile, protected template and issuer commit together.
func (a *Admin) EnsureAccount(ctx context.Context, actor int, issuer string) (int, error) {
	if issuer == "" {
		return 0, fmt.Errorf("%w: issuer is required", ErrInvalidSettings)
	}
	tx, s, err := a.begin(ctx, actor, issuer)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if s.AccountID > 0 {
		var valid bool
		err = tx.QueryRow(ctx, `SELECT enabled AND role='user' AND NOT local_password_login_enabled FROM users WHERE id=$1`, s.AccountID).Scan(&valid)
		if err != nil {
			return 0, err
		}
		if !valid {
			return 0, fmt.Errorf("%w: shared account is not eligible", ErrInvalidSettings)
		}
		return s.AccountID, tx.Commit(ctx)
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_login_identifiers WHERE identifier=$1)`, SharedUsername).Scan(&exists); err != nil {
		return 0, err
	}
	if exists {
		return 0, fmt.Errorf("%w: reserved preview name is already in use", ErrInvalidSettings)
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return 0, err
	}
	disabled := false
	p := auth.NewAccountProvisioner(auth.NewUserRepository(a.pool), pgstore.NewPostgresProvider(a.pool))
	account, err := p.CreateAccountInTransaction(ctx, tx, auth.CreateAccountInput{
		User:           models.CreateUserInput{Username: SharedUsername, Email: SharedUsername + "@preview.invalid", Password: base64.RawURLEncoding.EncodeToString(random[:]), Role: "user", LocalPasswordLoginEnabled: &disabled, Permissions: []string{}},
		DefaultProfile: auth.DefaultProfileOptions{Enabled: true, Name: "Preview"},
	})
	if err != nil {
		return 0, err
	}
	var profileID string
	if err = tx.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id=$1 AND is_primary`, account.ID).Scan(&profileID); err != nil {
		return 0, err
	}
	templates := entitlements.NewTemplateStore(a.pool)
	var templateRevision int64
	if err = tx.QueryRow(ctx, `SELECT current_revision FROM entitlement_templates WHERE key='bloem-preview'`).Scan(&templateRevision); err != nil {
		return 0, err
	}
	template, err := templates.GetInTx(ctx, tx, accesspolicy.PreviewTemplateKey, templateRevision)
	if err != nil {
		return 0, err
	}
	if _, err = templates.ApplyDefaultAccountTemplateInTransaction(ctx, tx, actor, account.ID, accesspolicy.PreviewTemplateKey, template.Revision, false); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_profile_onboarding(user_id,profile_id,tour_id,completed_at,updated_at) VALUES($1,$2,'welcome',$3,$3)`, account.ID, profileID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return 0, err
	}
	s.AccountID, s.Issuer = account.ID, issuer
	if err = a.store.putInTransaction(ctx, tx, s); err != nil {
		return 0, err
	}
	return s.AccountID, tx.Commit(ctx)
}

// Configure keeps account identity read-only and refuses both the configured
// canonical address and the normal address serving this admin request.
func (a *Admin) Configure(ctx context.Context, actor int, next Settings, servedHost string) error {
	tx, current, err := a.begin(ctx, actor, next.Issuer)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if next.Issuer == "" || current.AccountID == 0 || next.AccountID != current.AccountID {
		return fmt.Errorf("%w: ensure the shared account first; account identity is read-only", ErrInvalidSettings)
	}
	if err = a.configureInTransaction(ctx, tx, next, servedHost); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *Admin) configureInTransaction(ctx context.Context, tx pgx.Tx, next Settings, servedHost string) error {
	var canonical string
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT value FROM server_settings WHERE key='server.public_url'),'')`).Scan(&canonical); err != nil {
		return err
	}
	u, _ := url.Parse(canonical)
	canonicalHost := ""
	if u != nil {
		canonicalHost = auth.HostOnly(u.Host)
	}
	if next.Host != "" && (next.Host == canonicalHost || next.Host == auth.HostOnly(servedHost)) {
		return fmt.Errorf("%w: preview host already serves the application", ErrInvalidSettings)
	}
	for _, configured := range a.servedHosts {
		if u, err := url.Parse(configured); err == nil && u.Host != "" {
			configured = u.Host
		}
		if next.Host != "" && next.Host == auth.HostOnly(configured) {
			return fmt.Errorf("%w: preview host already serves the application", ErrInvalidSettings)
		}
	}
	return a.store.putInTransaction(ctx, tx, next)
}

func (a *Admin) RotateKeys(ctx context.Context, actor int, issuer string, keys []ed25519.PublicKey) error {
	if issuer == "" {
		return ErrInvalidSettings
	}
	tx, s, err := a.begin(ctx, actor, issuer)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	s.TicketPublicKeys = keys
	if s.AccountID == 0 {
		return ErrUnavailable
	}
	if err = a.store.putInTransaction(ctx, tx, s); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *Admin) Revoke(ctx context.Context, actor int, issuer string) error {
	if issuer == "" {
		return ErrInvalidSettings
	}
	tx, _, err := a.begin(ctx, actor, issuer)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = revokeGuests(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type Capacity struct {
	Enabled      bool `json:"enabled"`
	AccountReady bool `json:"account_ready"`
	Active       int  `json:"active"`
	Maximum      int  `json:"maximum"`
}

const sharedAccountReadySQL = `EXISTS(SELECT 1 FROM users u
 JOIN organization_memberships m ON m.account_id=u.id AND m.status='active'
 JOIN organizations o ON o.id=m.organization_id AND o.is_default
 JOIN access_groups g ON g.id=u.access_group_id AND g.organization_id=m.organization_id
 JOIN user_profiles p ON p.user_id=u.id AND p.organization_id=m.organization_id AND p.access_group_id=g.id AND p.is_primary
 WHERE u.id=$1 AND u.enabled AND u.role='user' AND NOT u.local_password_login_enabled
 AND g.managed_template_key='bloem-preview' AND g.library_ids IS NOT NULL)`

func (a *Admin) Capacity(ctx context.Context, actor int) (Capacity, error) {
	tx, s, err := a.begin(ctx, actor, "")
	if err != nil {
		return Capacity{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out := Capacity{Enabled: s.Enabled, Maximum: s.MaxConcurrent}
	if err = tx.QueryRow(ctx, `SELECT `+sharedAccountReadySQL+`, (SELECT count(*) FROM auth_sessions s JOIN bloem_preview_guest_sessions g ON g.session_id=s.id WHERE s.user_id=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND COALESCE(s.last_seen_at,s.created_at)>now()-make_interval(secs=>g.idle_ttl_seconds))`, s.AccountID).Scan(&out.AccountReady, &out.Active); err != nil {
		return Capacity{}, err
	}
	return out, nil
}

func (a *Admin) Verify(ctx context.Context, actor int, raw string) (Ticket, error) {
	audience, err := a.serverID.Resolve(ctx)
	if err != nil {
		return Ticket{}, err
	}
	tx, s, err := a.begin(ctx, actor, "")
	if err != nil {
		return Ticket{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if s.AccountID <= 0 {
		return Ticket{}, ErrUnavailable
	}
	var eligible bool
	if err = tx.QueryRow(ctx, `SELECT `+sharedAccountReadySQL, s.AccountID).Scan(&eligible); err != nil {
		return Ticket{}, err
	}
	if !eligible {
		return Ticket{}, ErrUnavailable
	}
	return VerifyTicket(raw, s.TicketPublicKeys, Expectation{Issuer: s.Issuer, Audience: audience, Host: s.Host}, time.Now())
}

func (a *Admin) SetLibraries(ctx context.Context, actor int, issuer string, libraries []int) (int64, error) {
	if issuer == "" || libraries == nil {
		return 0, ErrInvalidSettings
	}
	tx, s, err := a.begin(ctx, actor, issuer)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if s.AccountID == 0 {
		return 0, ErrUnavailable
	}
	templates := entitlements.NewTemplateStore(a.pool)
	revision, err := templates.RevisePreviewLibrariesInTransaction(ctx, tx, libraries)
	if err != nil {
		return 0, err
	}
	if _, err = templates.ApplyDefaultAccountTemplateInTransaction(ctx, tx, actor, s.AccountID, accesspolicy.PreviewTemplateKey, revision, false); err != nil {
		return 0, err
	}
	return revision, tx.Commit(ctx)
}
