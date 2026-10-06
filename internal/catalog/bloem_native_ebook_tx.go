package catalog

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/idgen"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

// NativeEbookPeopleTx reads credits within an already host-authorized publication.
// The caller owns the transaction and locks the media item before changing credits.
func (r *ItemRepository) NativeEbookPeopleTx(ctx context.Context, tx pgx.Tx, contentID string) ([]models.ItemPerson, error) {
	rows, err := tx.Query(ctx, `SELECT p.id,p.name,p.sort_name,p.bio,p.birth_date,p.death_date,p.birthplace,p.homepage,
 p.photo_path,p.photo_source_path,p.photo_thumbhash,p.tmdb_id,p.imdb_id,p.tvdb_id,p.plex_guid,p.created_at,p.updated_at,
 ip.kind,ip.character,ip.sort_order FROM item_people ip JOIN people p ON p.id=ip.person_id
 WHERE ip.content_id=$1 ORDER BY ip.kind,ip.sort_order`, contentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItemPeople(rows)
}

// ReplaceNativeEbookPeopleTx keeps credits and their search event in the same
// publication transaction. It preserves the normal (person,kind,character) dedup.
func (r *ItemRepository) ReplaceNativeEbookPeopleTx(ctx context.Context, tx pgx.Tx, contentID string, people []models.ItemPerson) error {
	if _, err := tx.Exec(ctx, "DELETE FROM item_people WHERE content_id=$1", contentID); err != nil {
		return err
	}
	type key struct {
		id        int64
		kind      models.PersonKind
		character string
	}
	seen := map[key]bool{}
	for _, p := range people {
		k := key{p.ID, p.Kind, p.Character}
		if seen[k] {
			continue
		}
		seen[k] = true
		id, err := idgen.NextID()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO item_people(id,content_id,person_id,kind,character,sort_order) VALUES($1,$2,$3,$4,$5,$6)
 ON CONFLICT(content_id,person_id,kind,character) DO UPDATE SET sort_order=EXCLUDED.sort_order`, id, contentID, p.ID, p.Kind, p.Character, p.SortOrder); err != nil {
			return err
		}
	}
	return r.searchIndexEvents.EnqueueUpsert(ctx, tx, contentID)
}

// FindNativeEbookAuthorTx is the name-only branch of author resolution. Native
// files supply no provider IDs or portraits. Call with names sorted to preserve
// lock ordering when publications from different folders share authors.
func (r *PersonRepository) FindNativeEbookAuthorTx(ctx context.Context, tx pgx.Tx, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("empty ebook author")
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "native-ebook-author:"+strings.ToLower(name)); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRow(ctx, "SELECT id FROM people WHERE lower(name)=lower($1) ORDER BY id LIMIT 1", name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	textID, err := idgen.NextID()
	if err != nil {
		return 0, err
	}
	id, err = strconv.ParseInt(textID, 10, 64)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO people(id,name) VALUES($1,$2)", id, name)
	return id, err
}
