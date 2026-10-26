package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SiloSwitchingReport describes what EnableSiloSwitching changed.
type SiloSwitchingReport struct {
	AlreadyEnabled     bool
	MembershipsCreated int
	RolesReconciled    int
}

// SiloSwitchingEnabled reports whether the database is kept switchable, so
// upstream Silo and Bloem can each serve it, one at a time.
func SiloSwitchingEnabled(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	if pool == nil {
		return false, errors.New("tenancy: silo switching status requires a database pool")
	}
	var enabled bool
	if err := pool.QueryRow(ctx, `SELECT public.bloem_silo_switching_enabled()`).Scan(&enabled); err != nil {
		return false, fmt.Errorf("tenancy: read silo switching: %w", err)
	}
	return enabled, nil
}

// EnableSiloSwitching makes the database switchable. Account policy already
// lives on users, where Silo reads it, so what remains is what Silo cannot
// honor: it refuses a second organization or a profile with its own login.
// Once enabled, triggers keep refusing both, place the accounts and profiles
// Silo creates in the default organization, and keep each account's role and
// its membership's in step.
func EnableSiloSwitching(ctx context.Context, pool *pgxpool.Pool) (SiloSwitchingReport, error) {
	var report SiloSwitchingReport
	if pool == nil {
		return report, errors.New("tenancy: enable silo switching requires a database pool")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return report, fmt.Errorf("tenancy: begin enable silo switching: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT enabled FROM public.bloem_silo_switching WHERE singleton FOR UPDATE`).Scan(&enabled); err != nil {
		return report, fmt.Errorf("tenancy: read silo switching: %w", err)
	}
	if enabled {
		report.AlreadyEnabled = true
		return report, tx.Commit(ctx)
	}
	var organizations, directLogins int
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM organizations),
		       (SELECT count(*) FROM user_profiles WHERE login_email IS NOT NULL OR password_hash IS NOT NULL)`,
	).Scan(&organizations, &directLogins); err != nil {
		return report, fmt.Errorf("tenancy: silo switching preflight: %w", err)
	}
	if organizations != 1 {
		return report, fmt.Errorf("tenancy: silo switching needs exactly one organization, found %d", organizations)
	}
	if directLogins != 0 {
		return report, fmt.Errorf("tenancy: silo switching cannot keep %d profiles with a direct profile login", directLogins)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		SELECT public.bloem_default_organization_id(), users.id, 'active',
		       CASE WHEN users.role = 'admin' THEN 'admin' ELSE 'user' END
		FROM users
		WHERE NOT EXISTS (SELECT 1 FROM organization_memberships m WHERE m.account_id = users.id)`)
	if err != nil {
		return report, fmt.Errorf("tenancy: create missing default memberships: %w", err)
	}
	report.MembershipsCreated = int(tag.RowsAffected())
	tag, err = tx.Exec(ctx, `
		UPDATE organization_memberships AS m
		SET legacy_role = CASE WHEN u.role = 'admin' THEN 'admin' ELSE 'user' END,
		    security_revision = m.security_revision + 1, updated_at = now()
		FROM users AS u
		WHERE m.account_id = u.id
		  AND m.organization_id = public.bloem_default_organization_id()
		  AND (m.legacy_role = 'admin') IS DISTINCT FROM (u.role = 'admin')`)
	if err != nil {
		return report, fmt.Errorf("tenancy: reconcile membership roles from users: %w", err)
	}
	report.RolesReconciled = int(tag.RowsAffected())
	if _, err := tx.Exec(ctx, `UPDATE public.bloem_silo_switching SET enabled = true, updated_at = now() WHERE singleton`); err != nil {
		return report, fmt.Errorf("tenancy: enable silo switching: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return report, fmt.Errorf("tenancy: commit enable silo switching: %w", err)
	}
	return report, nil
}

// DisableSiloSwitching lifts the switching guards, so the database can hold
// more organizations and profile logins. Silo must not serve it afterwards.
func DisableSiloSwitching(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("tenancy: disable silo switching requires a database pool")
	}
	if _, err := pool.Exec(ctx, `UPDATE public.bloem_silo_switching SET enabled = false, updated_at = now() WHERE singleton`); err != nil {
		return fmt.Errorf("tenancy: disable silo switching: %w", err)
	}
	return nil
}
