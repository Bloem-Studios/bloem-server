package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Households while stock Silo serves a switchable database (Bloem household
// logins). Stock Silo knows nothing of households. The database keeps each
// login's own enabled state in step with its main account's, which stock Silo
// honors (migration 20261122213808_bloem_household_logins_under_silo_switching),
// but stock Silo does not enforce a household's shared pool: each account
// plays to its own limits. A household of shares already fits its budget
// account by account. In a pooled household the main account carries the
// whole budget and each login may carry as much, so PrepareForSilo holds the
// logins of every pooled household before stock Silo serves: the household
// plays on its main account and profiles, within its budget, until Bloem
// serves again and ResumeAfterSilo releases them.

// SiloHouseholds counts the database's households for silo-switching status.
type SiloHouseholds struct {
	// Households are main accounts with at least one login; Logins are
	// those logins.
	Households, Logins int
	// Pooled are the households with a shared pool, and PooledLogins their
	// logins: the ones PrepareForSilo holds.
	Pooled, PooledLogins int
	// Held are logins held for stock Silo right now.
	Held int
}

// SiloSwitchingHouseholds counts the database's households.
func SiloSwitchingHouseholds(ctx context.Context, pool *pgxpool.Pool) (SiloHouseholds, error) {
	var h SiloHouseholds
	if pool == nil {
		return h, errors.New("tenancy: silo switching households requires a database pool")
	}
	err := pool.QueryRow(ctx, `
		SELECT count(DISTINCT login.household_main_user_id), count(*),
		       count(DISTINCT login.household_main_user_id) FILTER (WHERE pooled.main_user_id IS NOT NULL),
		       count(*) FILTER (WHERE pooled.main_user_id IS NOT NULL),
		       (SELECT count(*) FROM bloem_household_silo_holds)
		FROM users AS login
		LEFT JOIN bloem_household_pools AS pooled ON pooled.main_user_id = login.household_main_user_id
		    AND (pooled.max_streams > 0 OR pooled.max_transcodes > 0)
		WHERE login.household_main_user_id IS NOT NULL`).
		Scan(&h.Households, &h.Logins, &h.Pooled, &h.PooledLogins, &h.Held)
	if err != nil {
		return h, fmt.Errorf("tenancy: count households: %w", err)
	}
	return h, nil
}

// PrepareForSilo holds the logins of every pooled household so stock Silo can
// serve the database: each is switched off where stock Silo reads it
// (users.enabled) and its sign-ins end, and it is listed in
// bloem_household_silo_holds so Bloem gives it back. A login an
// administrator disabled is left alone; one the household switched off is
// held as it is. It refuses unless switching is on, the only state stock Silo
// may serve. It returns how many logins it newly held; running it again is
// safe.
func PrepareForSilo(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	if pool == nil {
		return 0, errors.New("tenancy: prepare for Silo requires a database pool")
	}
	held := 0
	err := withSiloSwitchingLock(ctx, pool, func(tx pgx.Tx, enabled bool) error {
		if !enabled {
			return errors.New("tenancy: silo switching is off, so stock Silo must not serve this database; enable it first")
		}
		var ids []int
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(array_agg(login.id ORDER BY login.id), '{}')
			FROM users AS login
			JOIN bloem_household_pools AS pooled ON pooled.main_user_id = login.household_main_user_id
			WHERE (pooled.max_streams > 0 OR pooled.max_transcodes > 0)
			  AND (login.enabled OR login.household_disabled)
			  AND NOT EXISTS (SELECT 1 FROM bloem_household_silo_holds h WHERE h.user_id = login.id)`).Scan(&ids); err != nil {
			return fmt.Errorf("tenancy: find pooled logins: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		// An administrator-shaped write: the login keeps no household mark
		// of its own while held unless the household had switched it off.
		if _, err := tx.Exec(ctx, `
			UPDATE users SET enabled = false, access_policy_revision = access_policy_revision + 1, updated_at = now()
			WHERE id = ANY($1) AND enabled`, ids); err != nil {
			return fmt.Errorf("tenancy: switch pooled logins off: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO bloem_household_silo_holds (user_id) SELECT unnest($1::int[])`, ids); err != nil {
			return fmt.Errorf("tenancy: record held logins: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT bloem_household_revoke_sign_ins($1)`, ids); err != nil {
			return fmt.Errorf("tenancy: end held logins' sign-ins: %w", err)
		}
		held = len(ids)
		return nil
	})
	return held, err
}

// ResumeAfterSilo releases the logins PrepareForSilo held, for Bloem serving
// again: each comes back as its household is now, enabled under an enabled
// main account and switched off by the household under a suspended one, so
// it returns with its main account. A login an administrator wrote while
// stock Silo served has already left the hold. Bloem runs it when it starts;
// it returns how many logins it released.
func ResumeAfterSilo(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	if pool == nil {
		return 0, errors.New("tenancy: resume after Silo requires a database pool")
	}
	released := 0
	err := withSiloSwitchingLock(ctx, pool, func(tx pgx.Tx, _ bool) error {
		rows, err := tx.Query(ctx, `
			SELECT h.user_id, COALESCE(main.enabled, false)
			FROM bloem_household_silo_holds AS h
			JOIN users AS login ON login.id = h.user_id
			LEFT JOIN users AS main ON main.id = login.household_main_user_id
			ORDER BY h.user_id FOR UPDATE OF h`)
		if err != nil {
			return fmt.Errorf("tenancy: read held logins: %w", err)
		}
		type held struct {
			id          int
			mainEnabled bool
		}
		logins, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (held, error) {
			var h held
			return h, row.Scan(&h.id, &h.mainEnabled)
		})
		if err != nil {
			return fmt.Errorf("tenancy: read held logins: %w", err)
		}
		for _, login := range logins {
			// Under a suspended main account only the household's mark is
			// written: a write of enabled would read as an administrator's
			// and clear it.
			statement := `UPDATE users SET household_disabled = true, updated_at = now() WHERE id = $1`
			if login.mainEnabled {
				statement = `UPDATE users SET enabled = true, household_disabled = false,
					access_policy_revision = access_policy_revision + 1, updated_at = now() WHERE id = $1`
			}
			if _, err := tx.Exec(ctx, statement, login.id); err != nil {
				return fmt.Errorf("tenancy: release held login %d: %w", login.id, err)
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM bloem_household_silo_holds`); err != nil {
			return fmt.Errorf("tenancy: clear held logins: %w", err)
		}
		released = len(logins)
		return nil
	})
	return released, err
}

// withSiloSwitchingLock runs fn in a transaction holding the switching row,
// so a prepare, a resume and an enable or disable never interleave.
func withSiloSwitchingLock(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx, bool) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tenancy: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT enabled FROM public.bloem_silo_switching WHERE singleton FOR UPDATE`).Scan(&enabled); err != nil {
		return fmt.Errorf("tenancy: read silo switching: %w", err)
	}
	if err := fn(tx, enabled); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("tenancy: commit: %w", err)
	}
	return nil
}
