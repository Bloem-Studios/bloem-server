package migrations_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/migrations"
)

func TestNativeOnboardingMigrationContract(t *testing.T) {
	data, err := fs.ReadFile(migrations.FS, "sql/20261006145307_bloem_native_storage_onboarding.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("single transactional Up/Down required")
	}
	up, down := parts[0], parts[1]
	if strings.Contains(up, "NO TRANSACTION") {
		t.Fatal("offline import must be atomic")
	}
	identities := map[string]string{
		"admin_playback_history": "media_item_id", "user_downloads": "media_item_id", "downloads": "content_id,episode_id",
		"user_watch_history": "media_item_id", "user_watch_progress": "media_item_id", "user_favorites": "media_item_id",
		"user_watchlist": "media_item_id", "user_ratings": "media_item_id", "user_personal_collection_items": "media_item_id",
		"library_collection_items": "media_item_id", "user_home_item_dismissals": "media_item_id,series_id",
		"user_history_hidden_items": "media_item_id", "user_audio_preferences": "series_id", "user_subtitle_preferences": "series_id",
		"user_series_playback_preferences": "series_id", "user_dropped_series": "series_id", "watch_provider_rating_items": "media_item_id",
		"watch_provider_dropped_items": "series_id", "ebook_reader_progress": "content_id", "media_item_provider_ids": "content_id,item_type",
	}
	for table, columns := range identities {
		pattern := "CREATE TRIGGER bloem_native_" + table + "_identity BEFORE UPDATE OF " + columns + " ON public." + table +
			" FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity("
		if !strings.Contains(up, pattern) {
			t.Errorf("finite identity site absent: %s (%s)", table, columns)
		}
	}
	for _, table := range []string{"user_dropped_series", "watch_provider_dropped_items"} {
		expected := "CREATE TRIGGER bloem_native_" + table + "_book BEFORE INSERT OR UPDATE ON public." + table +
			" FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_book_drop();"
		if !strings.Contains(up, expected) {
			t.Errorf("all-event book guard absent: %s", table)
		}
	}
	if !strings.Contains(up, "CREATE TRIGGER bloem_native_media_extras_book BEFORE INSERT OR UPDATE ON public.media_extras FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_extra();") {
		t.Fatal("independent extra-parent writer unprotected")
	}
	deferred := regexp.MustCompile(`CREATE CONSTRAINT TRIGGER [^\n]+`).FindAllString(up, -1)
	if len(deferred) != 5 {
		t.Fatalf("deferred inventory %d", len(deferred))
	}
	for _, line := range deferred {
		if !strings.Contains(line, "DEFERRABLE INITIALLY DEFERRED") {
			t.Fatal("deferred completion weakened")
		}
	}
	if !strings.Contains(up, "bloem_native_bloem_native_publication_permits_complete AFTER INSERT ON public.bloem_native_publication_permits") {
		t.Fatal("item/member-only omission can evade captured event")
	}
	permit := regexp.MustCompile(`(?s)CREATE TABLE public.bloem_native_publication_permits \((.*?)\n\);`).FindStringSubmatch(up)
	if len(permit) != 2 {
		t.Fatal("permit schema absent")
	}
	for _, forbidden := range []string{"content_id", "media_item_id", "series_id", "REFERENCES public.media_items", "REFERENCES public.media_files"} {
		if strings.Contains(permit[1], forbidden) {
			t.Fatalf("early allocation permit requires premature catalog identity: %s", forbidden)
		}
	}
	for _, field := range []string{
		"xid bigint PRIMARY KEY", "item_key text NOT NULL", "existing_item_key text", "item_version timestamptz",
		"folder_key bigint NOT NULL", "library_revision bigint NOT NULL", "folder_owner_id uuid NOT NULL", "source_key uuid NOT NULL",
		"source_owner_id uuid NOT NULL", "binding_id uuid NOT NULL", "discovery_run_id uuid NOT NULL", "installation_key bigint NOT NULL",
		"runtime_generation bigint NOT NULL", "configuration_revision bigint NOT NULL", "protocol_version integer NOT NULL",
		"ingestion_owner text NOT NULL", "ingestion_epoch bigint NOT NULL", "lease_token uuid NOT NULL",
		"entry_id text NOT NULL", "entry_revision text NOT NULL", "logical_path text NOT NULL", "catalog_location text NOT NULL",
		"entry_kind integer NOT NULL", "entry_size bigint NOT NULL", "entry_modified_unix_nano bigint NOT NULL",
		"normalized_modified_at timestamptz NOT NULL", "parsed_container text NOT NULL", "content_group_key text NOT NULL",
		"file_shape jsonb NOT NULL", "sidecar_shape jsonb NOT NULL", "retained_file_key bigint", "attempted_file_key bigint", "stored_file_key bigint",
	} {
		if !strings.Contains(permit[1], field) {
			t.Errorf("exact permit field absent: %s", field)
		}
	}
	firstDrop := strings.Index(down, "DROP TRIGGER")
	if firstDrop < 0 {
		t.Fatal("owned teardown absent")
	}
	refusal := down[:firstDrop]
	if !strings.Contains(refusal, "IN ACCESS EXCLUSIVE MODE") {
		t.Fatal("Down lacks quiescent exclusion")
	}
	for _, table := range strings.Fields("bloem_native_libraries bloem_native_publication_permits bloem_storage_bindings bloem_storage_file_refs bloem_storage_sources bloem_storage_installations bloem_storage_entries bloem_storage_scan_runs bloem_storage_scan_directories bloem_storage_scan_cursors bloem_storage_ingestion") {
		if !strings.Contains(refusal, "EXISTS(SELECT 1 FROM public."+table+")") {
			t.Errorf("Down omits retained category %s", table)
		}
	}
	if strings.Contains(refusal, "lease_until") || strings.Contains(refusal, "complete=") {
		t.Fatal("expired/completed evidence excluded from Down")
	}
}
