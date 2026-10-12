package scanner

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

// resolveStoragePeopleTx shares the ebook resolver and its lock order with native audio.
func resolveStoragePeopleTx(ctx context.Context, tx pgx.Tx, names map[string]string) (map[string]int64, error) {
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Concurrent publishers resolving the same new author must not create it
	// twice; locks are taken in sorted order to avoid deadlocks.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('native-ebook-author:' || k, 0))
		FROM unnest($1::text[]) WITH ORDINALITY AS t(k, n) ORDER BY n`, keys); err != nil {
		return nil, fmt.Errorf("lock ebook authors: %w", err)
	}
	people := map[string]int64{}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (lower(name)) lower(name), id FROM people
		WHERE lower(name) = ANY($1) ORDER BY lower(name), id`, keys)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		var id int64
		if err := rows.Scan(&key, &id); err != nil {
			rows.Close()
			return nil, err
		}
		people[key] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var newIDs []int64
	var newNames []string
	for _, key := range keys {
		if _, ok := people[key]; ok {
			continue
		}
		id, err := nextNumericID()
		if err != nil {
			return nil, err
		}
		people[key] = id
		newIDs = append(newIDs, id)
		newNames = append(newNames, names[key])
	}
	if len(newIDs) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO people (id, name) SELECT * FROM unnest($1::bigint[], $2::text[])`, newIDs, newNames); err != nil {
			return nil, fmt.Errorf("insert ebook authors: %w", err)
		}
	}
	return people, nil
}
