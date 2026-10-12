package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// groupLookupCapture records the content-group lookup a publication runs.
type groupLookupCapture struct {
	mu   sync.Mutex
	sql  string
	args []any
}

func (c *groupLookupCapture) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "GROUP BY content_group_key") {
		c.mu.Lock()
		c.sql, c.args = data.SQL, append([]any(nil), data.Args...)
		c.mu.Unlock()
	}
	return ctx
}

func (*groupLookupCapture) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// The per-page lookup of existing content groups must bound its index scan to
// the page's keys. Without the ebook group key version the folder-group index
// can only be read across the whole library on every page.
func TestStorageEbookGroupLookupUsesFolderGroupIndex(t *testing.T) {
	pool, s, folder, location := storageEbookFixture(t)
	ctx := t.Context()
	suffix := fmt.Sprint(time.Now().UnixNano())
	var entries []*storagev1.Entry
	for i := range 40 {
		entries = append(entries, storageEbookEntry(fmt.Sprintf("seed-%d", i), "epub",
			&storagev1.EbookMetadata{Title: fmt.Sprintf("Seed %d %s", i, suffix), Authors: []string{"Seed Writer"}}, "sha256:seed"))
	}
	publishStorageEbooks(t, pool, s, folder, location, entries...)
	if _, err := pool.Exec(ctx, `ANALYZE media_files`); err != nil {
		t.Fatal(err)
	}

	config, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	capture := &groupLookupCapture{}
	config.ConnConfig.Tracer = capture
	traced, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)
	// A new book of an existing item: its group key is unresolved on this page.
	book, ok := StorageEbookFromEntry(storageEbookEntry("seed-7", "pdf",
		&storagev1.EbookMetadata{Title: "Seed 7 " + suffix, Authors: []string{"Seed Writer"}}, "sha256:pdf"))
	if !ok {
		t.Fatal("missing metadata")
	}
	tx, err := traced.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := s.PublishStorageEbooksTx(ctx, tx, folder, location, 1, []StorageEbook{book}); err != nil {
		t.Fatal(err)
	}
	capture.mu.Lock()
	sql, args := capture.sql, capture.args
	capture.mu.Unlock()
	if sql == "" {
		t.Fatal("content-group lookup did not run")
	}
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	var plan []byte
	if err := tx.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+sql, args...).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	var doc []struct {
		Plan map[string]any `json:"Plan"`
	}
	if err := json.Unmarshal(plan, &doc); err != nil || len(doc) != 1 {
		t.Fatalf("plan %s: %v", plan, err)
	}
	var bounded bool
	var walk func(node map[string]any)
	walk = func(node map[string]any) {
		cond, _ := node["Index Cond"].(string)
		if node["Index Name"] == "idx_media_files_folder_group" && strings.Contains(cond, "group_key_version") && strings.Contains(cond, "content_group_key") {
			bounded = true
		}
		children, _ := node["Plans"].([]any)
		for _, child := range children {
			if c, ok := child.(map[string]any); ok {
				walk(c)
			}
		}
	}
	walk(doc[0].Plan)
	if !bounded {
		t.Fatalf("content-group lookup does not bound idx_media_files_folder_group by version and key:\n%s\n%s", sql, plan)
	}
}
