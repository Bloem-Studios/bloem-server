package catalog

import (
	"bytes"
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/Silo-Server/silo-server/migrations"
)

const nativeOnboardingMigration = "sql/20261006145307_bloem_native_storage_onboarding.sql"

var nativeFolderBookkeeping = strings.Fields("last_scanned_at scan_warning_code scan_warning_message scan_warning_at")
var nativeFolderProtected = strings.Fields("id type name enabled allow_empty_cleanup_once poster_path sort_order metadata_language chapter_thumbnails_enabled intro_detection_enabled collection_ungrouped_sort_order auto_translate_metadata trailer_kinds owner_id realtime_monitoring trickplay_enabled")

type nativeTriggerContract struct {
	table, function    string
	events             int
	columns, arguments []string
	deferred           bool
}
type nativeFunctionContract struct{ body, argument, result string }

func nativeSchemaContracts() (map[string]nativeTriggerContract, map[string]nativeFunctionContract, bool) {
	raw, err := migrations.FS.Open(nativeOnboardingMigration)
	if err != nil {
		return nil, nil, false
	}
	defer raw.Close()
	var data bytes.Buffer
	if _, err = data.ReadFrom(raw); err != nil {
		return nil, nil, false
	}
	up := strings.Split(data.String(), "-- +goose Down")[0]
	triggers := map[string]nativeTriggerContract{}
	functions := map[string]nativeFunctionContract{}
	fn := regexp.MustCompile(`(?s)CREATE FUNCTION public\.(bloem_native_\w+)\(([^)]*)\) RETURNS (\w+) LANGUAGE plpgsql VOLATILE AS \$\$(.*?)\$\$;`)
	for _, m := range fn.FindAllStringSubmatch(up, -1) {
		argument := ""
		if fields := strings.Fields(m[2]); len(fields) == 2 {
			argument = fields[1]
		}
		functions[m[1]] = nativeFunctionContract{strings.TrimSpace(m[4]), argument, m[3]}
	}
	tr := regexp.MustCompile(`CREATE (CONSTRAINT )?TRIGGER (bloem_native_\w+) (.*?) ON public\.(\w+)( DEFERRABLE INITIALLY DEFERRED)? FOR EACH ROW EXECUTE FUNCTION public\.(bloem_native_\w+)\((.*?)\);`)
	tables := map[string]bool{}
	for _, m := range tr.FindAllStringSubmatch(up, -1) {
		c := nativeTriggerContract{table: m[4], function: m[6], events: 1, deferred: m[1] != ""}
		events := m[3]
		if strings.HasPrefix(events, "BEFORE ") {
			c.events |= 2
		}
		if strings.Contains(events, "INSERT") {
			c.events |= 4
		}
		if strings.Contains(events, "DELETE") {
			c.events |= 8
		}
		if strings.Contains(events, "UPDATE") {
			c.events |= 16
		}
		if idx := strings.Index(events, "UPDATE OF "); idx >= 0 {
			c.columns = strings.Split(events[idx+10:], ",")
			sort.Strings(c.columns)
		}
		if m[7] != "" {
			for _, a := range strings.Split(m[7], ",") {
				c.arguments = append(c.arguments, strings.Trim(a, "'"))
			}
		}
		if c.deferred != (m[5] != "") {
			return nil, nil, false
		}
		triggers[m[2]] = c
		tables[c.table] = true
	}
	return triggers, functions, len(triggers) == 51 && len(functions) == 19 && len(tables) == 43
}

// NativeStorageSchemaReady checks the installed definitions, events, columns,
// enabled status, deferrability and keyed lookup indexes. This maintenance gate
// grants no actor authority and does not prove a working onboarding lifecycle.
func NativeStorageSchemaReady(ctx context.Context, q nativeModeQueryer) bool {
	if q == nil {
		return false
	}
	// A detached source needs its private latest marked-installation witness.
	// Inspect the public column and validated exact positive check; a default,
	// non-bigint/nonnullable field or deletion-coupled FK is not this contract.
	var lineageReady bool
	if q.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM pg_attribute a
 WHERE a.attrelid=to_regclass('public.bloem_storage_sources')
 AND a.attname='latest_installation_id' AND a.attnum>0 AND NOT a.attisdropped
 AND a.atttypid='pg_catalog.int8'::regtype AND a.atttypmod=-1
 AND NOT a.attnotnull AND NOT a.atthasdef AND a.attidentity='' AND a.attgenerated=''
 AND EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid=a.attrelid
  AND c.conname='bloem_storage_sources_latest_installation_positive'
  AND c.contype='c' AND c.convalidated
  AND pg_get_expr(c.conbin,c.conrelid)='((latest_installation_id IS NULL) OR (latest_installation_id > 0))')
 AND NOT EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid=a.attrelid
  AND c.contype='f' AND a.attnum=ANY(c.conkey)))`).Scan(&lineageReady) != nil || !lineageReady {
		return false
	}
	expected, functions, ok := nativeSchemaContracts()
	if !ok {
		return false
	}
	rows, err := q.Query(ctx, `SELECT t.tgname,c.relname,p.proname,p.oid::bigint,pn.nspname,t.tgtype::integer,t.tgenabled::text,
 t.tgdeferrable,t.tginitdeferred,t.tgargs,t.tgqual IS NULL,
 COALESCE((SELECT array_agg(a.attname::text ORDER BY a.attname) FROM pg_attribute a
 WHERE a.attrelid=t.tgrelid AND a.attnum=ANY(t.tgattr::smallint[])),ARRAY[]::text[])
 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_proc p ON p.oid=t.tgfoid JOIN pg_namespace pn ON pn.oid=p.pronamespace WHERE n.nspname='public' AND t.tgname LIKE 'bloem_native_%' AND NOT t.tgisinternal`)
	if err != nil {
		return false
	}
	seen := 0
	invokedFunctions := map[string]int64{}
	for rows.Next() {
		var name, table, fn, namespace, enabled string
		var functionOID int64
		var events int
		var deferred, initial, unconditional bool
		var args []byte
		var columns []string
		if err = rows.Scan(&name, &table, &fn, &functionOID, &namespace, &events, &enabled, &deferred, &initial, &args, &unconditional, &columns); err != nil {
			rows.Close()
			return false
		}
		want, exists := expected[name]
		actualArgs := []string(nil)
		zero := string([]byte{0})
		if len(args) > 0 {
			actualArgs = strings.Split(strings.TrimRight(string(args), zero), zero)
		}
		if !exists || want.table != table || want.function != fn || namespace != "public" || want.events != events || enabled != "O" || deferred != want.deferred || initial != want.deferred || !unconditional ||
			strings.Join(columns, ",") != strings.Join(want.columns, ",") || strings.Join(actualArgs, ",") != strings.Join(want.arguments, ",") {
			rows.Close()
			return false
		}
		if prior, exists := invokedFunctions[fn]; exists && prior != functionOID {
			rows.Close()
			return false
		}
		invokedFunctions[fn] = functionOID
		seen++
	}
	err = rows.Err()
	rows.Close()
	if err != nil || seen != len(expected) {
		return false
	}
	rows, err = q.Query(ctx, `SELECT p.oid::bigint,p.proname,p.prosrc,p.provolatile::text,p.prorettype::regtype::text,
 oidvectortypes(p.proargtypes),p.prosecdef,p.prokind::text FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname='public' AND p.proname LIKE 'bloem_native_%'`)
	if err != nil {
		return false
	}
	seen = 0
	verifiedFunctions := map[string]int64{}
	for rows.Next() {
		var name, body, volatility, result, argument, kind string
		var functionOID int64
		var security bool
		if err = rows.Scan(&functionOID, &name, &body, &volatility, &result, &argument, &security, &kind); err != nil {
			rows.Close()
			return false
		}
		want, exists := functions[name]
		if !exists || strings.TrimSpace(body) != want.body || argument != want.argument || result != want.result || volatility != "v" || security || kind != "f" {
			rows.Close()
			return false
		}
		verifiedFunctions[name] = functionOID
		seen++
	}
	err = rows.Err()
	rows.Close()
	if err != nil || seen != len(functions) {
		return false
	}
	// Tie every actual trigger target to the public signature/body verified
	// above; a same-name function or overload is not the installed guard.
	for name, oid := range invokedFunctions {
		if verifiedFunctions[name] != oid {
			return false
		}
	}
	rows, err = q.Query(ctx, `SELECT attname::text FROM pg_attribute WHERE attrelid='public.media_folders'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attname`)
	if err != nil {
		return false
	}
	var columns []string
	for rows.Next() {
		var col string
		if rows.Scan(&col) != nil {
			rows.Close()
			return false
		}
		columns = append(columns, col)
	}
	err = rows.Err()
	rows.Close()
	allowed := append(append([]string(nil), nativeFolderProtected...), nativeFolderBookkeeping...)
	sort.Strings(allowed)
	if err != nil || strings.Join(columns, ",") != strings.Join(allowed, ",") {
		return false
	}
	var unique bool
	if q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid
 WHERE c.relname='bloem_native_binding_folder_unique' AND i.indrelid='public.bloem_storage_bindings'::regclass
 AND i.indisunique AND i.indisvalid AND i.indisready AND i.indpred IS NULL AND i.indnkeyatts=1
 AND i.indkey[0]=(SELECT attnum FROM pg_attribute WHERE attrelid=i.indrelid AND attname='folder_id'))`).Scan(&unique) != nil || !unique {
		return false
	}
	for _, index := range []struct{ table, columns string }{
		{"media_files", "file_path"}, {"media_files", "id"}, {"media_files", "content_id"}, {"media_files", "media_folder_id"}, {"media_files", "episode_id"}, {"media_files", "extra_id"},
		{"media_items", "content_id"}, {"media_folders", "id"}, {"media_item_libraries", "content_id,media_folder_id"}, {"media_item_libraries", "media_folder_id"},
		{"bloem_storage_file_refs", "media_file_id"}, {"bloem_storage_file_refs", "binding_id,entry_id"}, {"bloem_storage_bindings", "folder_id"},
		{"bloem_storage_sources", "key"}, {"bloem_storage_entries", "source_key,entry_id"}, {"bloem_storage_ingestion", "run_id,binding_id"},
		{"media_folder_paths", "media_folder_id"}, {"user_dropped_series", "series_id"}, {"watch_provider_dropped_items", "series_id"},
		{"media_item_roots", "content_id"}, {"media_item_groups", "content_id"},
		{"media_extras", "content_id"}, {"media_extras", "parent_id"}, {"episodes", "content_id"}, {"seasons", "content_id"},
		{"bloem_native_libraries", "folder_id"}, {"bloem_native_libraries", "creation_key"}, {"bloem_native_publication_permits", "xid"},
	} {
		var indexed bool
		err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_index i WHERE i.indrelid=to_regclass('public.'||$1)
 AND i.indisvalid AND i.indisready AND i.indnkeyatts >= $3
 AND (i.indpred IS NULL OR pg_get_expr(i.indpred,i.indrelid)='('||split_part($2,',',1)||' IS NOT NULL)')
 AND (SELECT string_agg(a.attname,',' ORDER BY k.ordinality)
 FROM unnest(i.indkey::smallint[]) WITH ORDINALITY k(attnum,ordinality)
 JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.attnum WHERE k.ordinality<=$3)=$2)`,
			index.table, index.columns, len(strings.Split(index.columns, ","))).Scan(&indexed)
		if err != nil || !indexed {
			return false
		}
	}
	return true
}
