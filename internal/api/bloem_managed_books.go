package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func (s *managedCalendarSource) readManagedBooks(ctx context.Context, q managedtracking.LibraryQuery, scope access.Scope) (managedtracking.LibraryData, error) {
	out := managedtracking.LibraryData{}
	ids := []string{}
	if q.ParentID != "" {
		ids = append(ids, q.ParentID)
	} else {
		rows, err := s.pool.Query(ctx, `SELECT content_id FROM bloem_companion_book_clock WHERE user_id=$1 AND profile_id=$2 AND content_id>$3 ORDER BY content_id LIMIT $4`, scope.UserID, scope.ProfileID, q.Position, q.Limit+1)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return out, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		if len(ids) > q.Limit {
			out.Next = ids[q.Limit-1]
			ids = ids[:q.Limit]
		}
	}
	items, err := catalog.NewItemRepository(s.pool).GetByIDsWithAccess(ctx, ids, managedLibraryFilter(scope))
	if err != nil {
		return out, err
	}
	visible := map[string]*models.MediaItem{}
	for _, item := range items {
		visible[item.ContentID] = item
	}
	for _, id := range ids {
		item := visible[id]
		if item == nil {
			if q.ParentID != "" {
				return out, managedtracking.ErrAuthority
			}
			continue
		}
		if item.Type != "ebook" && item.Type != "audiobook" {
			return out, managedtracking.ErrSnapshot
		}
		state, err := s.managedBookState(ctx, scope, item)
		if err != nil {
			if q.ParentID != "" {
				return out, err
			}
			continue
		}
		out.BookStates = append(out.BookStates, state)
	}
	return out, nil
}
func (s *managedCalendarSource) managedBookState(ctx context.Context, scope access.Scope, item *models.MediaItem) (*library.BookState, error) {
	state := &library.BookState{ContentId: item.ContentID, Kind: item.Type, Title: item.Title, ExternalIds: libraryItem(item).ExternalIds, Reset_: true}
	var file *int64
	var changed time.Time
	err := s.pool.QueryRow(ctx, `SELECT revision,file_id,reset,changed_at FROM bloem_companion_book_clock WHERE user_id=$1 AND profile_id=$2 AND content_id=$3`, scope.UserID, scope.ProfileID, item.ContentID).Scan(&state.Revision, &file, &state.Reset_, &changed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		state.ChangedAt = timestamppb.New(changed)
	}
	if item.Type == "ebook" {
		var actualFile int64
		var at time.Time
		err = s.pool.QueryRow(ctx, `SELECT file_id,location,progress,updated_at FROM ebook_reader_progress WHERE user_id=$1 AND profile_id=$2 AND content_id=$3`, scope.UserID, scope.ProfileID, item.ContentID).Scan(&actualFile, &state.EbookLocation, &state.EbookFraction, &at)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			file = &actualFile
			state.Reset_ = false
			state.Completed = state.EbookFraction >= models.EbookFinishedProgressThreshold
		}
	} else {
		var at time.Time
		err = s.pool.QueryRow(ctx, `SELECT position_seconds,duration_seconds,completed,updated_at FROM user_watch_progress WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3`, scope.UserID, scope.ProfileID, item.ContentID).Scan(&state.AudiobookSeconds, &state.AudiobookDurationSeconds, &state.Completed, &at)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			state.Reset_ = false
		}
	}
	files, err := scanner.NewFileRepository(s.pool).GetByContentID(ctx, item.ContentID)
	if err != nil {
		return nil, err
	}
	selected := []*models.MediaFile{}
	for _, f := range catalog.FilterMediaFilesByAccess(files, managedLibraryFilter(scope)) {
		if f.MissingSince != nil {
			continue
		}
		if file != nil && item.Type == "ebook" && int64(f.ID) != *file {
			continue
		}
		if item.Type == "ebook" && f.Container != "epub" && f.Container != "pdf" {
			continue
		}
		if len(f.FilePath) >= 14 && f.FilePath[:14] == "bloem-storage:" {
			if _, _, e := storagesource.NewRepository(s.pool).FileReference(ctx, f.ID, f.MediaFolderID); e != nil {
				continue
			}
		}
		selected = append(selected, f)
	}
	if len(selected) == 0 || item.Type == "ebook" && len(selected) != 1 {
		return nil, managedtracking.ErrSnapshot
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })
	if item.Type == "audiobook" {
		selected, state.AudiobookDurationSeconds, err = managedAudiobookEdition(selected)
		if err != nil {
			return nil, err
		}
	}
	signatures := []any{}
	for _, f := range selected {
		signatures = append(signatures, []any{f.ID, f.FileSize, f.FileHash, f.FileModifiedAt, f.EditionKey, f.Duration, f.MediaFolderID, f.PresentationGroupKey, f.PresentationPartIndex, f.PresentationPartTotal})
	}
	raw, _ := json.Marshal(signatures)
	digest := sha256.Sum256(raw)
	state.EditionId = hex.EncodeToString(digest[:])
	state.FileId = int64(selected[0].ID)
	identity := []*library.LibraryItem{libraryItem(item)}
	if err = s.addManagedLibraryIdentities(ctx, identity); err != nil {
		return nil, err
	}
	state.ExternalIds = identity[0].ExternalIds
	state.Isbn = state.ExternalIds["isbn"]
	return state, nil
}

// The companion cannot choose between alternate recordings. Accept a single
// file or one complete native multipart timeline, using the host's part order.
func managedAudiobookEdition(files []*models.MediaFile) ([]*models.MediaFile, float64, error) {
	if len(files) == 0 {
		return nil, 0, managedtracking.ErrSnapshot
	}
	parts := append([]*models.MediaFile(nil), files...)
	for _, f := range parts {
		if f == nil || f.Duration <= 0 {
			return nil, 0, managedtracking.ErrSnapshot
		}
	}
	first := parts[0]
	if len(parts) > 1 || first.PresentationPartTotal > 1 {
		if first.PresentationKind != "multipart" || first.PresentationGroupKey == "" || first.PresentationPartTotal != len(parts) {
			return nil, 0, managedtracking.ErrSnapshot
		}
		sort.Slice(parts, func(i, j int) bool { return parts[i].PresentationPartIndex < parts[j].PresentationPartIndex })
		for i, f := range parts {
			if f.PresentationKind != "multipart" || f.PresentationGroupKey != first.PresentationGroupKey || f.MediaFolderID != first.MediaFolderID || f.EditionKey != first.EditionKey || f.PresentationPartTotal != len(parts) || f.PresentationPartIndex != i+1 {
				return nil, 0, managedtracking.ErrSnapshot
			}
		}
	}
	var duration float64
	for _, f := range parts {
		duration += float64(f.Duration)
	}
	return parts, duration, nil
}
func (s *managedCalendarSource) mutateManagedBook(ctx context.Context, tx pgx.Tx, in *library.LibraryMutation, scope access.Scope) (*library.LibraryMutationReceipt, error) {
	state := in.BookState
	if state == nil || state.ContentId != in.TargetId || state.ChangedAt == nil || state.ChangedAt.CheckValid() != nil || state.ChangedAt.AsTime().After(time.Now()) || in.Copy != nil || len(in.Items) != 0 {
		return nil, managedtracking.ErrSnapshot
	}
	// Different event IDs may target the same missing progress row. Serialize
	// companion writers without changing the native progress/clock lock order.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "bloem.book:"+scope.ProfileID+":"+in.TargetId); err != nil {
		return nil, err
	}
	items, err := catalog.NewItemRepository(s.pool).GetByIDsWithAccess(ctx, []string{in.TargetId}, managedLibraryFilter(scope))
	if err != nil {
		return nil, err
	}
	if len(items) != 1 {
		return nil, managedtracking.ErrAuthority
	}
	current, err := s.managedBookState(ctx, scope, items[0])
	if err != nil {
		return nil, err
	}
	if state.Kind != current.Kind || state.EditionId != current.EditionId || state.FileId != current.FileId || state.Isbn != current.Isbn {
		return nil, managedtracking.ErrSnapshot
	}
	if in.ExpectedRevision != current.Revision {
		return nil, managedtracking.ErrConflict
	}
	if !scope.PlaybackAllowed {
		return nil, managedtracking.ErrAuthority
	}
	var at time.Time
	table, key := "ebook_reader_progress", "content_id"
	if state.Kind == "audiobook" {
		table, key = "user_watch_progress", "media_item_id"
	}
	err = tx.QueryRow(ctx, `SELECT updated_at FROM `+table+` WHERE user_id=$1 AND profile_id=$2 AND `+key+`=$3 FOR UPDATE`, scope.UserID, scope.ProfileID, in.TargetId).Scan(&at)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil && !state.ChangedAt.AsTime().After(at) {
		return &library.LibraryMutationReceipt{EventId: in.EventId, TargetId: in.TargetId, Revision: current.Revision, Status: "no_change"}, nil
	}
	// Recheck the native revision after locking the actual progress row. Native
	// writers acquire progress before the clock trigger, so lock order is preserved.
	var revision uint64
	var clockAt time.Time
	err = tx.QueryRow(ctx, `SELECT revision,changed_at FROM bloem_companion_book_clock WHERE user_id=$1 AND profile_id=$2 AND content_id=$3`, scope.UserID, scope.ProfileID, in.TargetId).Scan(&revision, &clockAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if revision != in.ExpectedRevision {
		return nil, managedtracking.ErrConflict
	}
	if !clockAt.IsZero() && !state.ChangedAt.AsTime().After(clockAt) {
		return &library.LibraryMutationReceipt{EventId: in.EventId, TargetId: in.TargetId, Revision: revision, Status: "no_change"}, nil
	}
	if state.Kind == "ebook" {
		store := &managedEbookProgressTx{tx: tx, eventAt: state.ChangedAt.AsTime()}
		switch in.Operation {
		case "book.completed", "book.reset":
			handler := handlers.NewItemsHandler(catalog.NewBrowseRepository(s.pool), catalog.NewItemRepository(s.pool), nil, nil, nil, scanner.NewFileRepository(s.pool), s.stores, nil, nil)
			handler.SetEbookReaderProgressStore(store)
			_, err = handler.SetWatchedState(ctx, scope.UserID, scope.ProfileID, in.TargetId, in.Operation == "book.completed", managedLibraryFilter(scope))
		case "book.progress":
			if state.EbookLocation == "" {
				return nil, managedtracking.ErrSnapshot
			}
			handler := handlers.NewEbookReaderHandler(&handlers.MediaFileAuthorizer{FileResolver: scanner.NewFileRepository(s.pool), ItemAccess: catalog.NewItemRepository(s.pool)})
			handler.ProgressStore = store
			_, err = handler.SaveReaderProgress(ctx, handlers.EbookReaderProgress{UserID: scope.UserID, ProfileID: scope.ProfileID, ContentID: in.TargetId, FileID: int(state.FileId), Location: state.EbookLocation, Progress: state.EbookFraction, UpdatedAt: state.ChangedAt.AsTime()}, managedLibraryFilter(scope))
		default:
			return nil, managedtracking.ErrSnapshot
		}
	} else {
		if state.AudiobookDurationSeconds != current.AudiobookDurationSeconds || state.EbookLocation != "" || state.EbookFraction != 0 {
			return nil, managedtracking.ErrSnapshot
		}
		if math.IsNaN(state.AudiobookSeconds) || math.IsInf(state.AudiobookSeconds, 0) || state.AudiobookSeconds < 0 || math.IsNaN(state.AudiobookDurationSeconds) || math.IsInf(state.AudiobookDurationSeconds, 0) || state.AudiobookDurationSeconds <= 0 || state.AudiobookSeconds > state.AudiobookDurationSeconds {
			return nil, managedtracking.ErrSnapshot
		}
		switch in.Operation {
		case "book.reset":
			_, err = tx.Exec(ctx, `DELETE FROM user_watch_progress WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3 AND updated_at<$4`, scope.UserID, scope.ProfileID, in.TargetId, state.ChangedAt.AsTime())
		case "book.completed", "book.progress":
			completed := in.Operation == "book.completed"
			position := state.AudiobookSeconds
			if completed {
				position = 0
			}
			_, err = tx.Exec(ctx, `INSERT INTO user_watch_progress(user_id,profile_id,media_item_id,position_seconds,duration_seconds,completed,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(user_id,profile_id,media_item_id) DO UPDATE SET position_seconds=CASE WHEN EXCLUDED.completed THEN 0 ELSE GREATEST(user_watch_progress.position_seconds,EXCLUDED.position_seconds) END,duration_seconds=GREATEST(user_watch_progress.duration_seconds,EXCLUDED.duration_seconds),completed=user_watch_progress.completed OR EXCLUDED.completed,updated_at=EXCLUDED.updated_at WHERE user_watch_progress.updated_at<EXCLUDED.updated_at`, scope.UserID, scope.ProfileID, in.TargetId, position, state.AudiobookDurationSeconds, completed, state.ChangedAt.AsTime())
		default:
			return nil, managedtracking.ErrSnapshot
		}
	}
	if err != nil {
		return nil, managedLibraryMutationError(err)
	}
	err = tx.QueryRow(ctx, `SELECT revision FROM bloem_companion_book_clock WHERE user_id=$1 AND profile_id=$2 AND content_id=$3`, scope.UserID, scope.ProfileID, in.TargetId).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) && in.Operation == "book.reset" {
		err = tx.QueryRow(ctx, `INSERT INTO bloem_companion_book_clock(user_id,profile_id,content_id,kind,file_id,reset) VALUES($1,$2,$3,$4,$5,true) RETURNING revision`, scope.UserID, scope.ProfileID, in.TargetId, state.Kind, state.FileId).Scan(&revision)
	}
	if err != nil {
		return nil, err
	}
	return &library.LibraryMutationReceipt{EventId: in.EventId, TargetId: in.TargetId, Revision: revision, Status: "applied"}, nil
}

type managedEbookProgressTx struct {
	tx      pgx.Tx
	eventAt time.Time
}

func (s *managedEbookProgressTx) Get(ctx context.Context, user int, profile, content string) (*handlers.EbookReaderProgress, error) {
	out := &handlers.EbookReaderProgress{UserID: user, ProfileID: profile, ContentID: content}
	err := s.tx.QueryRow(ctx, `SELECT file_id,location,progress,updated_at FROM ebook_reader_progress WHERE user_id=$1 AND profile_id=$2 AND content_id=$3`, user, profile, content).Scan(&out.FileID, &out.Location, &out.Progress, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return out, err
}
func (s *managedEbookProgressTx) Upsert(ctx context.Context, p handlers.EbookReaderProgress) error {
	return s.UpsertNewer(ctx, p)
}
func (s *managedEbookProgressTx) UpsertNewer(ctx context.Context, p handlers.EbookReaderProgress) error {
	at := s.eventAt
	if at.IsZero() {
		at = p.UpdatedAt
	}
	_, err := s.tx.Exec(ctx, `INSERT INTO ebook_reader_progress(user_id,profile_id,content_id,file_id,location,progress,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(user_id,profile_id,content_id) DO UPDATE SET file_id=EXCLUDED.file_id,location=EXCLUDED.location,progress=CASE WHEN ebook_reader_progress.progress >= $8 AND EXCLUDED.progress<$8 THEN ebook_reader_progress.progress ELSE EXCLUDED.progress END,updated_at=EXCLUDED.updated_at WHERE ebook_reader_progress.updated_at<EXCLUDED.updated_at`, p.UserID, p.ProfileID, p.ContentID, p.FileID, p.Location, p.Progress, at, models.EbookFinishedProgressThreshold)
	return err
}
func (s *managedEbookProgressTx) Delete(ctx context.Context, user int, profile, content string) error {
	_, err := s.tx.Exec(ctx, `DELETE FROM ebook_reader_progress WHERE user_id=$1 AND profile_id=$2 AND content_id=$3 AND updated_at<$4`, user, profile, content, s.eventAt)
	return err
}
func (s *managedEbookProgressTx) ListByContentIDs(ctx context.Context, user int, profile string, ids []string) (map[string]handlers.EbookReaderProgress, error) {
	out := map[string]handlers.EbookReaderProgress{}
	for _, id := range ids {
		p, err := s.Get(ctx, user, profile, id)
		if err != nil {
			return nil, err
		}
		if p != nil {
			out[id] = *p
		}
	}
	return out, nil
}
