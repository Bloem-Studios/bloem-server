package managedtracking

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
)

// LibraryQuery carries only host-issued continuation positions. SnapshotAt is
// the original creation fence, not the time of a later page request.
type LibraryQuery struct {
	View, ParentID, Query, Kind, Position string
	Limit                                 int
	SnapshotAt                            time.Time
}
type LibraryData struct {
	Items       []*library.LibraryItem
	Collections []*library.LibraryCollection
	Copies      []*library.LibraryCopy
	BookStates  []*library.BookState
	Next        string
	Revision    uint64
}
type LibrarySource interface {
	LibraryAuthority(context.Context, Profile) (string, error)
	ReadLibrary(context.Context, Profile, LibraryQuery) (LibraryData, error)
}
type libraryCursor struct {
	Installation              int
	Instance, Scope           string
	Generation                uint64
	Profile                   Profile
	Authority                 string
	View, Parent, Query, Kind string
	Limit                     uint32
	Position                  string
	Generated, Expires        time.Time
}

// ReadLibrary releases the initial grant/profile locks before domain callbacks,
// then reacquires and rechecks authority before publishing any metadata.
func (s *Service) ReadLibrary(ctx context.Context, installation int, r *library.LibraryRequest) (*library.LibraryPage, error) {
	if s == nil || s.Pool == nil || s.Cipher == nil {
		return nil, ErrAuthority
	}
	if r == nil || !positiveID(r.AccountId) || !availabilityText(r.ProfileId) {
		return nil, ErrAuthority
	}
	if r.Limit < 1 || r.Limit > 100 || len(r.Query) > 256 || len(r.ParentId) > 256 || len(r.Cursor) > 16384 {
		return nil, ErrSnapshot
	}
	switch r.View {
	case "catalog", "collections", "collection_items", "copies", "book_states":
	default:
		return nil, ErrSnapshot
	}
	switch r.Kind {
	case "", "movie", "series", "ebook", "audiobook":
	default:
		return nil, ErrSnapshot
	}
	source, ok := s.calendarSource().(LibrarySource)
	if !ok {
		return nil, ErrAuthority
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	info, err := authority(ctx, tx, installation, grantRead)
	if err != nil {
		return nil, err
	}
	p, err := calendarProfile(ctx, tx, info.Scope, r.AccountId, r.ProfileId)
	if err != nil {
		return nil, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	digest, err := source.LibraryAuthority(ctx, p)
	if err != nil {
		return nil, err
	}
	if digest == "" {
		return nil, ErrAuthority
	}
	c := libraryCursor{Installation: installation, Instance: info.InstanceID, Scope: info.Scope, Generation: info.Generation, Profile: p, Authority: digest, View: r.View, Parent: r.ParentId, Query: r.Query, Kind: r.Kind, Limit: r.Limit, Generated: now, Expires: now.Add(120 * time.Second)}
	if r.Cursor != "" {
		raw, e := s.Cipher.Decrypt(r.Cursor, "bloem.library.cursor.v1")
		if e != nil || json.Unmarshal([]byte(raw), &c) != nil {
			return nil, ErrSnapshot
		}
		if c.Installation != installation || c.Instance != info.InstanceID || c.Scope != info.Scope || c.Generation != info.Generation || c.Profile != p || c.Authority != digest {
			return nil, ErrAuthority
		}
		if c.View != r.View || c.Parent != r.ParentId || c.Query != r.Query || c.Kind != r.Kind || c.Limit != r.Limit || c.Position == "" || !now.Before(c.Expires) {
			return nil, ErrSnapshot
		}
	}
	data, err := source.ReadLibrary(ctx, p, LibraryQuery{View: r.View, ParentID: r.ParentId, Query: r.Query, Kind: r.Kind, Position: c.Position, Limit: int(r.Limit), SnapshotAt: c.Generated})
	if err != nil {
		return nil, err
	}
	if len(data.Items) > int(r.Limit) || len(data.Collections) > int(r.Limit) || len(data.Copies) > int(r.Limit) || len(data.BookStates) > int(r.Limit) || len(data.Next) > 4096 || data.Next != "" && data.Next == c.Position {
		return nil, ErrSnapshot
	}
	if r.View == "collection_items" && (len(data.Collections) != 1 || data.Collections[0] == nil || data.Collections[0].Id != r.ParentId) {
		return nil, ErrSnapshot
	}
	if err = validateLibraryData(r.View, data); err != nil {
		return nil, err
	}
	out := &library.LibraryPage{InstanceId: info.InstanceID, AccountId: p.AccountID, ProfileId: p.ProfileID, View: r.View, ParentId: r.ParentId, GeneratedAt: timestamppb.New(c.Generated), ExpiresAt: timestamppb.New(c.Expires), Complete: data.Next == "", Items: data.Items, Collections: data.Collections, Copies: data.Copies, BookStates: data.BookStates, Revision: data.Revision}
	if proto.Size(out) > 1<<20 {
		return nil, ErrSnapshot
	}
	if data.Next != "" {
		c.Position = data.Next
		raw, e := json.Marshal(c)
		if e != nil {
			return nil, e
		}
		out.NextCursor, err = s.Cipher.Encrypt(string(raw), "bloem.library.cursor.v1")
		if err != nil {
			return nil, err
		}
	}
	tx, err = s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	current, err := authority(ctx, tx, installation, grantShare)
	if err != nil {
		return nil, err
	}
	fresh, err := calendarProfile(ctx, tx, current.Scope, r.AccountId, r.ProfileId)
	if err != nil {
		return nil, err
	}
	after, err := source.LibraryAuthority(ctx, fresh)
	if err != nil {
		return nil, err
	}
	if current != info || fresh != p || after != digest {
		return nil, ErrAuthority
	}
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, err
	}
	if !now.Before(c.Expires) {
		return nil, ErrSnapshot
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

type libraryBroker struct {
	library.UnimplementedLibraryHostServer
	service      *Service
	installation int
}

func (b *libraryBroker) Read(ctx context.Context, r *library.LibraryRequest) (*library.LibraryPage, error) {
	out, err := b.service.ReadLibrary(ctx, b.installation, r)
	return out, safeRPC(err)
}

func validateLibraryData(view string, data LibraryData) error {
	if view != "catalog" && view != "collection_items" && len(data.Items) > 0 || view != "collections" && view != "collection_items" && len(data.Collections) > 0 || view != "copies" && len(data.Copies) > 0 || view != "book_states" && len(data.BookStates) > 0 {
		return ErrSnapshot
	}
	seen := map[string]bool{}
	for _, item := range data.Items {
		if item == nil || !availabilityText(item.ContentId) || item.Title == "" || len(item.Title) > 4096 || seen[item.ContentId] {
			return ErrSnapshot
		}
		switch item.Kind {
		case "movie", "series", "ebook", "audiobook":
		default:
			return ErrSnapshot
		}
		for key, value := range item.ExternalIds {
			switch key {
			case "tmdb", "imdb", "tvdb", "isbn", "asin":
			default:
				return ErrSnapshot
			}
			if !availabilityText(value) {
				return ErrSnapshot
			}
		}
		seen[item.ContentId] = true
	}
	for _, c := range data.Collections {
		if c == nil || !availabilityText(c.Id) || c.Name == "" || len(c.Name) > 4096 || len(c.Description) > 10000 || c.Revision == 0 || seen[c.Id] {
			return ErrSnapshot
		}
		switch c.Scope {
		case "personal":
		case "shared", "server":
			if c.Writable {
				return ErrSnapshot
			}
		default:
			return ErrSnapshot
		}
		seen[c.Id] = true
	}

	for _, copy := range data.Copies {
		if copy == nil || !availabilityText(copy.Id) || copy.Title == "" || copy.Revision == 0 || seen[copy.Id] {
			return ErrSnapshot
		}
		seen[copy.Id] = true
	}

	for _, state := range data.BookStates {
		if state == nil || !availabilityText(state.ContentId) || state.FileId < 1 || state.EditionId == "" || seen[state.ContentId] || len(state.EbookLocation) > 16384 {
			return ErrSnapshot
		}
		if state.Kind != "ebook" && state.Kind != "audiobook" {
			return ErrSnapshot
		}
		if state.ChangedAt != nil && state.ChangedAt.CheckValid() != nil || state.Revision > 0 && state.ChangedAt == nil {
			return ErrSnapshot
		}
		for _, value := range []float64{state.AudiobookSeconds, state.AudiobookDurationSeconds, state.EbookFraction} {
			if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				return ErrSnapshot
			}
		}
		if state.EbookFraction > 1 {
			return ErrSnapshot
		}
		seen[state.ContentId] = true
	}

	return nil
}
