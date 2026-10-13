package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
)

func validManagedCopy(c *library.LibraryCopy) bool {
	if c == nil || c.Id == "" || len(c.Id) > 128 || len(c.ContentId) > 128 || strings.TrimSpace(c.Title) == "" || len(c.Title) > 4096 || len(c.Edition) > 200 || len(c.Audio) > 100 || len(c.Store) > 200 || len(c.Location) > 200 || len(c.Notes) > 2000 || c.PriceCents < 0 || c.PriceCents > 100000000 || c.BitrateKbps > 1000000 || len(c.Custom) > 100 {
		return false
	}
	switch c.Kind {
	case "movie", "series", "ebook", "audiobook":
	default:
		return false
	}
	switch c.Format {
	case "digital", "uhd_bluray", "bluray", "dvd", "vhs", "hardcover", "paperback", "ebook", "audiobook", "vinyl", "cd", "cassette", "game_physical", "game_digital", "boxed", "other":
	default:
		return false
	}
	if c.Currency != "" && (len(c.Currency) != 3 || strings.Trim(c.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "") {
		return false
	}
	if c.AcquiredOn != "" {
		if _, err := time.Parse("2006-01-02", c.AcquiredOn); err != nil {
			return false
		}
	}
	for key, value := range c.Custom {
		if key == "" || len(key) > 128 || len(value) > 10000 {
			return false
		}
	}
	for key, value := range c.ExternalIds {
		switch key {
		case "tmdb", "imdb", "tvdb", "isbn", "asin":
		default:
			return false
		}
		if value == "" || len(value) > 128 {
			return false
		}
	}
	return true
}
func (s *managedCalendarSource) readManagedCopies(ctx context.Context, q managedtracking.LibraryQuery, scope access.Scope) (managedtracking.LibraryData, error) {
	out := managedtracking.LibraryData{}
	rows, err := s.pool.Query(ctx, `SELECT id,payload,revision,removed FROM bloem_companion_owned_copies WHERE user_id=$1 AND profile_id=$2 AND id>$3 ORDER BY id LIMIT $4`, scope.UserID, scope.ProfileID, q.Position, q.Limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		var revision uint64
		var removed bool
		if err = rows.Scan(&id, &raw, &revision, &removed); err != nil {
			return out, err
		}
		if len(out.Copies) == q.Limit {
			out.Next = out.Copies[len(out.Copies)-1].Id
			break
		}
		c := new(library.LibraryCopy)
		if err = json.Unmarshal(raw, c); err != nil {
			return out, err
		}
		c.Id = id
		c.Revision = revision
		c.Removed = removed
		out.Copies = append(out.Copies, c)
	}
	return out, rows.Err()
}
func (s *managedCalendarSource) mutateManagedCopy(ctx context.Context, tx pgx.Tx, in *library.LibraryMutation, scope access.Scope) (*library.LibraryMutationReceipt, error) {
	if in.TargetId == "" || len(in.TargetId) > 128 || len(in.Items) != 0 || in.BookState != nil {
		return nil, managedtracking.ErrSnapshot
	}
	var revision uint64
	var raw []byte
	// Serialize a new record too; existing native collection transactions use a
	// separate lane, and ownership remains independent of catalog availability.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "bloem.copy:"+scope.ProfileID+":"+in.TargetId); err != nil {
		return nil, err
	}
	err := tx.QueryRow(ctx, `SELECT revision,payload FROM bloem_companion_owned_copies WHERE user_id=$1 AND profile_id=$2 AND id=$3 FOR UPDATE`, scope.UserID, scope.ProfileID, in.TargetId).Scan(&revision, &raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if revision != in.ExpectedRevision {
		return nil, managedtracking.ErrConflict
	}
	var copy *library.LibraryCopy
	switch in.Operation {
	case "copy.upsert":
		if !validManagedCopy(in.Copy) || in.Copy.Id != in.TargetId || in.Copy.Removed {
			return nil, managedtracking.ErrSnapshot
		}
		var ok bool
		copy, ok = proto.Clone(in.Copy).(*library.LibraryCopy)
		if !ok {
			return nil, managedtracking.ErrSnapshot
		}
		if copy.ContentId != "" {
			items, e := catalog.NewItemRepository(s.pool).GetByIDsWithAccess(ctx, []string{copy.ContentId}, managedLibraryFilter(scope))
			if e != nil {
				return nil, e
			}
			if len(items) != 1 || items[0].Type != copy.Kind {
				return nil, managedtracking.ErrAuthority
			}
		}
	case "copy.remove":
		if revision == 0 || in.Copy != nil {
			return nil, managedtracking.ErrSnapshot
		}
		copy = new(library.LibraryCopy)
		if err = json.Unmarshal(raw, copy); err != nil {
			return nil, err
		}
		copy.Removed = true
	default:
		return nil, managedtracking.ErrSnapshot
	}
	copy.Revision = revision + 1
	raw, err = json.Marshal(copy)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_companion_owned_copies(user_id,profile_id,id,payload,revision,removed) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(user_id,profile_id,id) DO UPDATE SET payload=EXCLUDED.payload,revision=EXCLUDED.revision,removed=EXCLUDED.removed,changed_at=clock_timestamp()`, scope.UserID, scope.ProfileID, in.TargetId, raw, copy.Revision, copy.Removed)
	if err != nil {
		return nil, err
	}
	status := "applied"
	if copy.Removed {
		status = "removed"
	}
	return &library.LibraryMutationReceipt{EventId: in.EventId, TargetId: in.TargetId, Revision: copy.Revision, Status: status}, nil
}
