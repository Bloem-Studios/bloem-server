//go:build integration

package api

import (
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

func TestManagedEbookStateKeepsExactLocatorAndRejectsWrongEdition(t *testing.T) {
	pool := coverRecipeDatabase(t)
	ctx := t.Context()
	var user, folder, file int
	id := uuid.NewString()
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,enabled) VALUES($1,'synthetic','user',true) RETURNING id`, "book-"+id).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('ebooks','Synthetic book',true) RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{{`INSERT INTO media_items(content_id,type,title) VALUES($1,'ebook','Synthetic book')`, []any{id}}, {`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, []any{id, folder}}} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_files(content_id,media_folder_id,file_path,container,file_size) VALUES($1,$2,'/synthetic/book.epub','epub',1234) RETURNING id`, id, folder).Scan(&file); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ebook_reader_progress(user_id,profile_id,content_id,file_id,location,progress,updated_at) VALUES($1,'reader',$2,$3,'epubcfi(/6/2)',0.25,clock_timestamp()-interval '1 minute')`, user, id, file); err != nil {
		t.Fatal(err)
	}
	scope := access.Scope{UserID: user, ProfileID: "reader", ProfileVerified: true, PlaybackAllowed: true, AllowedLibraryIDs: []int{folder}}
	source := &managedCalendarSource{pool: pool, stores: pgstore.NewPostgresProvider(pool), resolver: managedCalendarResolverFixture{scope: scope}}
	p := managedtracking.Profile{AccountID: strconv.Itoa(user), ProfileID: "reader", AccountActive: true, Active: true}
	data, err := source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "book_states", ParentID: id, Limit: 20})
	if err != nil || len(data.BookStates) != 1 || data.BookStates[0].EbookLocation != "epubcfi(/6/2)" || data.BookStates[0].EbookFraction != 0.25 {
		t.Fatal(data, err)
	}
	state := proto.Clone(data.BookStates[0]).(*library.BookState)
	state.EditionId = "foreign"
	state.ChangedAt = timestamppb.New(time.Now())
	state.Completed = true
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	in := &library.LibraryMutation{Operation: "book.completed", EventId: "finished", TargetId: id, ExpectedRevision: data.BookStates[0].Revision, BookState: state}
	if _, err = source.MutateLibrary(ctx, p, tx, in); !errors.Is(err, managedtracking.ErrSnapshot) {
		t.Fatal("edition mismatch accepted", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	state.EditionId = data.BookStates[0].EditionId
	if _, err = source.MutateLibrary(ctx, p, tx, in); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	data, err = source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "book_states", ParentID: id, Limit: 20})
	if err != nil || len(data.BookStates) != 1 || !data.BookStates[0].Completed || data.BookStates[0].EbookLocation != "epubcfi(/6/2)" {
		t.Fatal("completion lost native locator", data, err)
	}
}

func TestManagedAudiobookRejectsNonFiniteDuration(t *testing.T) {
	pool := coverRecipeDatabase(t)
	ctx := t.Context()
	id := uuid.NewString()
	var user, folder int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,enabled) VALUES($1,'synthetic','user',true) RETURNING id`, "audio-"+id).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('audiobooks','Synthetic audio',true) RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title) VALUES($1,'audiobook','Synthetic audiobook')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, id, folder); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_files(content_id,media_folder_id,file_path,container,file_size,duration) VALUES($1,$2,'/synthetic/book.m4b','m4b',1234,3600)`, id, folder); err != nil {
		t.Fatal(err)
	}
	scope := access.Scope{UserID: user, ProfileID: "listener", ProfileVerified: true, PlaybackAllowed: true, AllowedLibraryIDs: []int{folder}}
	source := &managedCalendarSource{pool: pool, stores: pgstore.NewPostgresProvider(pool), resolver: managedCalendarResolverFixture{scope: scope}}
	p := managedtracking.Profile{AccountID: strconv.Itoa(user), ProfileID: "listener", AccountActive: true, Active: true}
	data, err := source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "book_states", ParentID: id, Limit: 20})
	if err != nil || len(data.BookStates) != 1 {
		t.Fatal(data, err)
	}
	for _, duration := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		state := proto.Clone(data.BookStates[0]).(*library.BookState)
		state.ChangedAt = timestamppb.Now()
		state.AudiobookSeconds = 10
		state.AudiobookDurationSeconds = duration
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = source.MutateLibrary(ctx, p, tx, &library.LibraryMutation{EventId: "invalid-duration", Operation: "book.progress", TargetId: id, ExpectedRevision: state.Revision, BookState: state})
		tx.Rollback(ctx)
		if !errors.Is(err, managedtracking.ErrSnapshot) {
			t.Fatalf("nonfinite duration accepted: %v, %v", duration, err)
		}
	}
}
