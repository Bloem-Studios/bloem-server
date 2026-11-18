package migrations

import "testing"

const (
	beforeSearchMatchingStrategyLast = 20261118014504
	searchMatchingStrategyLast       = 20261118161912
)

// TestSearchMatchingStrategyLastReplacesFrequency: a stored "frequency"
// matching strategy, which Silo's search settings reject, becomes "last";
// "all" and other settings stay as they are.
func TestSearchMatchingStrategyLastReplacesFrequency(t *testing.T) {
	for _, tc := range []struct{ stored, want string }{
		{"frequency", "last"},
		{" Frequency ", "last"},
		{"all", "all"},
		{"last", "last"},
	} {
		t.Run(tc.stored, func(t *testing.T) {
			db, provider := newTestDBMigratedTo(t, beforeSearchMatchingStrategyLast)
			exec(t, db,
				`DELETE FROM server_settings WHERE key IN ('catalog.search.meilisearch.matching_strategy', 'bloem.test.unrelated')`,
				`INSERT INTO server_settings (key, value) VALUES ('catalog.search.meilisearch.matching_strategy', '`+tc.stored+`')`,
				`INSERT INTO server_settings (key, value) VALUES ('bloem.test.unrelated', 'frequency')`,
			)
			if _, err := provider.UpTo(t.Context(), searchMatchingStrategyLast); err != nil {
				t.Fatal(err)
			}

			assertCount(t, db, `SELECT count(*) FROM server_settings
				WHERE key = 'catalog.search.meilisearch.matching_strategy' AND value = '`+tc.want+`'`, 1)
			assertCount(t, db, `SELECT count(*) FROM server_settings
				WHERE key = 'bloem.test.unrelated' AND value = 'frequency'`, 1)
		})
	}
}
