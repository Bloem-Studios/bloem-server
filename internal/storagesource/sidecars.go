package storagesource

import (
	"context"
	"strings"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// Keep this expression identical to the sidecar lookup migration. The parent
// is the literal prefix including the final slash, never a cleaned filesystem path.
const sidecarLiteralParent = `CASE WHEN strpos(logical_path,'/')=0 THEN '' ELSE regexp_replace(logical_path,'[^/]*$','') END`
const sidecarSuffix = `lower(substring(name FROM '\.[^.]*$'))`

// SiblingCandidates reads bounded sibling candidates from the current completed
// generation while holding the ingestion fence. Hashes keep long opaque paths
// out of Btree keys; raw comparisons make hash collisions harmless. MOBI still
// counts as an ebook when deciding whether a generic cover belongs to one book.
// literalDirectory includes its final slash when present; the bare root is empty.
func (r *Repository) SiblingCandidates(ctx context.Context, l IngestionLease, literalDirectory string, candidateNames, ebookSuffixes []string) ([]*storagev1.Entry, bool, error) {
	if !validText(literalDirectory, 65536, false) || len(candidateNames) > 128 || len(ebookSuffixes) > 128 {
		return nil, false, ErrCheckpointConflict
	}
	seen := make(map[string]bool, len(candidateNames))
	for _, name := range candidateNames {
		folded := strings.ToLower(name)
		if !validText(name, 4096, true) || seen[folded] {
			return nil, false, ErrCheckpointConflict
		}
		seen[folded] = true
	}
	suffixes := make([]string, 0, len(ebookSuffixes)+1)
	seen = make(map[string]bool, len(ebookSuffixes)+1)
	for _, suffix := range append(append([]string(nil), ebookSuffixes...), ".mobi") {
		suffix = strings.ToLower(suffix)
		if !validText(suffix, 4096, true) || !strings.HasPrefix(suffix, ".") || len(suffix) == 1 || strings.ContainsAny(suffix[1:], "./\\") {
			return nil, false, ErrCheckpointConflict
		}
		if !seen[suffix] {
			suffixes = append(suffixes, suffix)
			seen[suffix] = true
		}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = verifyIngestionLease(ctx, tx, l); err != nil {
		return nil, false, err
	}

	// A separate indexed lookup for each requested name stops after two matches,
	// allowing ambiguity detection without fetching any unrelated directory rows.
	rows, err := tx.Query(ctx, `SELECT c.ordinal,e.entry_id,e.name,e.logical_path,e.size,e.modified_unix_nano,e.revision
 FROM unnest($5::text[]) WITH ORDINALITY AS c(name,ordinal)
 CROSS JOIN LATERAL (
  SELECT entry_id,name,logical_path,size,modified_unix_nano,revision
  FROM bloem_storage_entries
  WHERE source_key=$1 AND last_seen_run=$2 AND configuration_revision=$3 AND kind=1
   AND absence_confirmed_at IS NULL
   AND md5(`+sidecarLiteralParent+`)=md5($4) AND (`+sidecarLiteralParent+`)=$4
   AND md5(lower(name))=md5(lower(c.name)) AND lower(name)=lower(c.name)
  LIMIT 2
 ) e`, l.SourceKey, l.RunID, l.ConfigurationRevision, literalDirectory, candidateNames)
	if err != nil {
		return nil, false, err
	}
	entries := make([]*storagev1.Entry, 0, len(candidateNames))
	matches := make(map[int64]bool, len(candidateNames))
	for rows.Next() {
		entry := &storagev1.Entry{Kind: storagev1.EntryKind_ENTRY_KIND_FILE}
		var ordinal int64
		if err = rows.Scan(&ordinal, &entry.Id, &entry.Name, &entry.LogicalPath, &entry.Size, &entry.ModifiedUnixNano, &entry.Revision); err != nil {
			rows.Close()
			return nil, false, err
		}
		if matches[ordinal] {
			rows.Close()
			return nil, false, ErrCheckpointConflict
		}
		matches[ordinal] = true
		entries = append(entries, entry)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}

	// Suffix lookups also stop at two, including unsupported ebooks. Neither a
	// directory nor a library is materialized just to decide generic cover use.
	var ebookCount int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM (
  SELECT e.present FROM unnest($5::text[]) AS s(suffix)
  CROSS JOIN LATERAL (
   SELECT 1 AS present FROM bloem_storage_entries
   WHERE source_key=$1 AND last_seen_run=$2 AND configuration_revision=$3 AND kind=1
    AND absence_confirmed_at IS NULL
    AND md5(`+sidecarLiteralParent+`)=md5($4) AND (`+sidecarLiteralParent+`)=$4
    AND md5(`+sidecarSuffix+`)=md5(lower(s.suffix)) AND `+sidecarSuffix+`=lower(s.suffix)
   LIMIT 2
  ) e
  LIMIT 2
 ) siblings`, l.SourceKey, l.RunID, l.ConfigurationRevision, literalDirectory, suffixes).Scan(&ebookCount)
	if err != nil {
		return nil, false, err
	}
	if err = ingestionLeaseActive(ctx, tx, l); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return entries, ebookCount == 1, nil
}
