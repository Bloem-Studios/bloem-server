package backup

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
)

// Bloem's backups hold user data only. Point-in-time recovery of the
// database covers a whole-server recovery, so a backup does not need the
// catalog, the embeddings or the scan state, which are most of the
// database. With the switch on, the dump keeps the data of the accounts,
// their profiles and every per-user table (a user_id or profile_id
// column), with the organizations, memberships and access groups they
// belong to, and leaves every other table's data out. The restore set,
// which a restore of chosen accounts reads, and the uploaded files are
// taken as Silo takes them. Such a backup cannot replace the live database
// (bloemRefuseWholeServerRestore).

var bloemUserDataOnly atomic.Bool

// SetBloemUserDataOnly makes the backups this process takes user-data
// backups. Bloem's server binary turns it on at start; it is off by default,
// so Silo's own tests keep taking whole-server backups.
func SetBloemUserDataOnly(on bool) { bloemUserDataOnly.Store(on) }

// bloemUserDataTables are kept beside the per-user tables: the accounts,
// and the organizations, memberships and groups they belong to.
var bloemUserDataTables = []string{tableUsers, "organizations", "organization_memberships", "access_groups"}

// bloemCatalogTable is left out of every user-data backup; a manifest that
// leaves its data out is one.
const bloemCatalogTable = "media_items"

// bloemExcludedTableData adds, for a user-data backup, every table that is
// not user data to the tables whose data the dump leaves out.
func bloemExcludedTableData(ctx context.Context, q queryer, excluded []string) ([]string, error) {
	if !bloemUserDataOnly.Load() {
		return excluded, nil
	}
	rows, err := q.Query(ctx, `
		SELECT c.relname::text, EXISTS (
			SELECT 1 FROM pg_attribute a
			WHERE a.attrelid = c.oid AND a.attname IN ('user_id', 'profile_id') AND a.attnum > 0 AND NOT a.attisdropped)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition
		ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("listing the tables a user-data backup leaves out: %w", err)
	}
	defer rows.Close()
	out := slices.Clone(excluded)
	for rows.Next() {
		var table string
		var perUser bool
		if err := rows.Scan(&table, &perUser); err != nil {
			return nil, err
		}
		if !perUser && !slices.Contains(bloemUserDataTables, table) && !slices.Contains(out, table) {
			out = append(out, table)
		}
	}
	return out, rows.Err()
}

// bloemRefuseWholeServerRestore refuses to replace the live database with a
// user-data backup, which holds no catalog. Its accounts are restored from
// the Backups page instead.
func bloemRefuseWholeServerRestore(m *Manifest) error {
	if m != nil && slices.Contains(m.ExcludedTableData, bloemCatalogTable) {
		return errors.New("this backup holds user data only, so it cannot replace this server's database: " +
			"restore accounts from it on the Backups page, and recover the whole database from its point-in-time recovery")
	}
	return nil
}
