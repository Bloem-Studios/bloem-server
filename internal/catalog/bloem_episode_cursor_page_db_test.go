package catalog

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The fast episode cursor (bloem_episode_cursor_page.go) must page exactly as
// the general query does, ties included, and write cursors the general query
// can continue. Two libraries send a definition to the general query; one
// library takes the fast path.
func TestBloemEpisodeCursorPageMatchesTheGeneralQueryPostgres(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	library, emptyLibrary, label := seedBloemEpisodeCursorLibrary(t, pool)

	executor := QueryExecutor{Pool: pool, Scope: "episode"}
	access := AccessFilter{AllowedLibraryIDs: []int{library, emptyLibrary}}
	read := func(def QueryDefinition, size int, cursor *QueryCursor, fast bool) ([]string, *QueryCursor, QueryCursorPage) {
		t.Helper()
		page, err := executor.PreviewCursorPage(ctx, def, access, size, cursor, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := executor.bloemEpisodeCursorPage(ctx, def, access, size, cursor, false); ok != fast {
			t.Fatalf("fast path taken = %v, want %v", ok, fast)
		}
		var got []string
		for _, item := range page.Items {
			got = append(got, label[item.ContentID])
		}
		return got, page.Next, page
	}
	for _, order := range []string{"desc", "asc"} {
		t.Run(order, func(t *testing.T) {
			fastDef := QueryDefinition{MediaScope: "episode", LibraryIDs: []int{library}, Sort: QuerySort{Field: querySortLastAirDate, Order: order}}
			generalDef := fastDef
			generalDef.LibraryIDs = []int{library, emptyLibrary}

			var general, fast []string
			var cursor *QueryCursor
			for range label {
				page, next, _ := read(generalDef, 2, cursor, false)
				general = append(general, page...)
				if cursor = next; cursor == nil {
					break
				}
			}
			cursor = nil
			for range label {
				page, next, _ := read(fastDef, 2, cursor, true)
				fast = append(fast, page...)
				if cursor = next; cursor == nil {
					break
				}
			}
			if strings.Join(fast, " ") != strings.Join(general, " ") || len(fast) != len(label) {
				t.Fatalf("fast order = %v, general order = %v", fast, general)
			}

			// A cursor the fast path wrote continues on the general query.
			first, next, page := read(fastDef, 3, nil, true)
			if page.Total != len(label) || !page.TotalExact {
				t.Fatalf("total = %d exact %v, want %d exact", page.Total, page.TotalExact, len(label))
			}
			rest := first
			for cursor := next; cursor != nil; {
				more, following, _ := read(generalDef, 3, cursor, false)
				rest = append(rest, more...)
				cursor = following
			}
			if strings.Join(rest, " ") != strings.Join(general, " ") {
				t.Fatalf("fast then general = %v, want %v", rest, general)
			}
		})
	}

	// Past the cap the total is reported as inexact.
	defer func(previous int) { bloemEpisodeCursorCountCap = previous }(bloemEpisodeCursorCountCap)
	bloemEpisodeCursorCountCap = 5
	_, _, page := read(QueryDefinition{MediaScope: "episode", LibraryIDs: []int{library}, Sort: QuerySort{Field: querySortReleaseDate, Order: "desc"}}, 2, nil, true)
	if page.Total != 5 || page.TotalExact {
		t.Fatalf("capped total = %d exact %v, want 5 inexact", page.Total, page.TotalExact)
	}
}

// seedBloemEpisodeCursorLibrary makes two series of English episodes with
// shared air dates and one unaired episode in one library, plus an empty
// library, and returns each episode's label by content ID.
func seedBloemEpisodeCursorLibrary(t *testing.T, pool *pgxpool.Pool) (int, int, map[string]string) {
	t.Helper()
	ctx := t.Context()
	stamp := time.Now().UnixNano()
	alpha := fmt.Sprintf("zz-bloem-cursor-%d", stamp)
	bravo := fmt.Sprintf("aa-bloem-cursor-%d", stamp)
	var library, emptyLibrary int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('series','bloem cursor',true) RETURNING id`).Scan(&library); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('series','bloem cursor empty',true) RETURNING id`).Scan(&emptyLibrary); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id = ANY($1)`, []string{alpha, bravo})
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_folders WHERE id = ANY($1)`, []int{library, emptyLibrary})
	})
	for _, series := range []struct{ id, title, language string }{{alpha, "Alpha Show", "en"}, {bravo, "Bravo Show", "en"}} {
		if _, err := pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title,status,genres,original_language) VALUES($1,'series',$2,'released','{Comedy}',$3)`, series.id, series.title, series.language); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, series.id, library); err != nil {
			t.Fatal(err)
		}
	}
	label := map[string]string{}
	for _, episode := range []struct {
		series, tag    string
		season, number int
		title, airDate string
	}{
		{alpha, "A", 3, 1, "Mixer", "2026-08-19"},
		{alpha, "A", 3, 2, "Zero Hour", "2026-08-19"},
		{alpha, "A", 3, 3, "All In", "2026-08-19"},
		{alpha, "A", 3, 9, "Meet the Family", "2026-08-26"},
		{alpha, "A", 3, 10, "Dinner", "2026-08-26"},
		{alpha, "A", 3, 11, "Reunion", "2026-08-30"},
		{bravo, "B", 1, 4, "Qualifying", "2026-08-26"},
		{bravo, "B", 1, 3, "Practice 3", "2026-08-26"},
		{bravo, "B", 1, 9, "Unaired", ""},
	} {
		id := episode.series + "-" + strings.ToLower(strings.ReplaceAll(episode.title, " ", ""))
		label[id] = fmt.Sprintf("%s%d.%d", episode.tag, episode.season, episode.number)
		var airDate any
		if episode.airDate != "" {
			airDate = episode.airDate
		}
		if _, err := pool.Exec(ctx, `INSERT INTO episodes(content_id,series_id,season_number,episode_number,title,air_date) VALUES($1,$2,$3,$4,$5,$6::date)`, id, episode.series, episode.season, episode.number, episode.title, airDate); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO episode_libraries(episode_id,media_folder_id,first_seen_at) VALUES($1,$2,'2026-08-01'::timestamptz)`, id, library); err != nil {
			t.Fatal(err)
		}
	}

	return library, emptyLibrary, label
}
