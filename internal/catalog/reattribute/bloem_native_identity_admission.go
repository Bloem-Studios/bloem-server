package reattribute

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

// requireLocalReattributionTx admits the requested identity remap before dedupe.
func requireLocalReattributionTx(ctx context.Context, tx pgx.Tx, opts Options) error {
	pairs := append([]IDPair{{From: opts.FromContentID, To: opts.ToContentID}}, opts.EpisodePairs...)
	keys := make([]string, 0, 2*len(pairs))
	for _, pair := range pairs {
		if pair.From == "" || pair.To == "" || pair.From == pair.To {
			return fmt.Errorf("reattribute: invalid id pair %q -> %q", pair.From, pair.To)
		}
		keys = append(keys, pair.From, pair.To)
	}
	sort.Strings(keys)
	keys = slices.Compact(keys)

	if err := requireNativeReattributionPhase(ctx, tx, opts, keys); err != nil {
		return err
	}
	if !catalog.NativeStorageSchemaReady(ctx, tx) {
		return &pgconn.PgError{Code: "BN003", Message: "Native storage admission unavailable"}
	}
	if _, err := tx.Exec(ctx, "SELECT public.bloem_native_lock_item_keys($1::text[])", keys); err != nil {
		return fmt.Errorf("reattribute: identity admission locks: %w", err)
	}
	if err := requireNativeReattributionPhase(ctx, tx, opts, keys); err != nil {
		return err
	}
	for _, key := range keys {
		var class string
		if err := tx.QueryRow(ctx, "SELECT public.bloem_native_item_class($1)", key).Scan(&class); err != nil {
			return fmt.Errorf("reattribute: identity admission classification: %w", err)
		}
		switch class {
		case "local":
		case "native", "inconsistent":
			return &pgconn.PgError{Code: "BN001", Message: "Native local operation unsupported"}
		default:
			return &pgconn.PgError{Code: "BN003", Message: "Native storage admission unavailable"}
		}
	}
	return nil
}
