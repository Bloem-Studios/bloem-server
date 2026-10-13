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

func TestManagedAudiobookRejectsInvalidDurationAndStaleResetReplay(t *testing.T) {
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
	for _, duration := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 7200, 1800} {
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
			t.Fatalf("invalid duration accepted: %v, %v", duration, err)
		}
	}
	// A reset removes the native progress row, but its clock still fences delayed
	// progress even when the companion has refreshed to the reset revision.
	state := proto.Clone(data.BookStates[0]).(*library.BookState)
	oldEvent := time.Now().Add(-time.Minute)
	state.ChangedAt = timestamppb.Now()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.MutateLibrary(ctx, p, tx, &library.LibraryMutation{EventId: "reset-empty", Operation: "book.reset", TargetId: id, ExpectedRevision: state.Revision, BookState: state})
	if err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	data, err = source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "book_states", ParentID: id, Limit: 20})
	if err != nil || len(data.BookStates) != 1 {
		t.Fatal(data, err)
	}
	state = proto.Clone(data.BookStates[0]).(*library.BookState)
	state.ChangedAt = timestamppb.New(oldEvent)
	state.AudiobookSeconds = 20
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	receipt, err := source.MutateLibrary(ctx, p, tx, &library.LibraryMutation{EventId: "late-progress", Operation: "book.progress", TargetId: id, ExpectedRevision: state.Revision, BookState: state})
	if err != nil || receipt.Status != "no_change" {
		t.Fatalf("delayed progress resurrected reset: %v, %v", receipt, err)
	}

	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Native activity newer than an imported event must survive both completion
	// and reset, even after the companion refreshed the current revision.
	if _, err = pool.Exec(ctx, `INSERT INTO user_watch_progress(user_id,profile_id,media_item_id,position_seconds,duration_seconds,completed,updated_at) VALUES($1,'listener',$2,120,3600,false,clock_timestamp())`, user, id); err != nil {
		t.Fatal(err)
	}
	data, err = source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "book_states", ParentID: id, Limit: 20})
	if err != nil || len(data.BookStates) != 1 {
		t.Fatal(data, err)
	}
	for _, operation := range []string{"book.completed", "book.reset"} {
		state = proto.Clone(data.BookStates[0]).(*library.BookState)
		state.ChangedAt = timestamppb.New(oldEvent)
		tx, err = pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err = source.MutateLibrary(ctx, p, tx, &library.LibraryMutation{EventId: operation, Operation: operation, TargetId: id, ExpectedRevision: state.Revision, BookState: state})
		tx.Rollback(ctx)
		if err != nil || receipt.Status != "no_change" {
			t.Fatalf("newer native activity overwritten: %v, %v", receipt, err)
		}
	}
	for _, operation := range []string{"book.completed", "book.reset"} {
		data, err = source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "book_states", ParentID: id, Limit: 20})
		if err != nil || len(data.BookStates) != 1 {
			t.Fatal(data, err)
		}
		state = proto.Clone(data.BookStates[0]).(*library.BookState)
		state.ChangedAt = timestamppb.Now()
		tx, err = pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = source.MutateLibrary(ctx, p, tx, &library.LibraryMutation{EventId: "current-" + operation, Operation: operation, TargetId: id, ExpectedRevision: state.Revision, BookState: state})
		if err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		after, e := source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "book_states", ParentID: id, Limit: 20})
		if e != nil || len(after.BookStates) != 1 {
			t.Fatal(after, e)
		}
		got := after.BookStates[0]
		if got.AudiobookDurationSeconds != 3600 || got.AudiobookSeconds != 0 || got.Completed != (operation == "book.completed") || got.Reset_ != (operation == "book.reset") {
			t.Fatalf("wrong explicit state after %s: %v", operation, got)
		}
	}

	// Two companion events with the same revision cannot both create the missing
	// row. Wait for the database lock, then commit the first event.
	data, err = source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "book_states", ParentID: id, Limit: 20})
	if err != nil || len(data.BookStates) != 1 {
		t.Fatal(data, err)
	}
	first := proto.Clone(data.BookStates[0]).(*library.BookState)
	first.ChangedAt = timestamppb.Now()
	first.AudiobookSeconds = 10
	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback(ctx)
	if _, err = source.MutateLibrary(ctx, p, tx1, &library.LibraryMutation{EventId: "first-progress", Operation: "book.progress", TargetId: id, ExpectedRevision: first.Revision, BookState: first}); err != nil {
		t.Fatal(err)
	}
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Rollback(ctx)
	second := proto.Clone(first).(*library.BookState)
	second.ChangedAt = timestamppb.Now()
	second.AudiobookSeconds = 30
	done := make(chan error, 1)
	go func() {
		_, e := source.MutateLibrary(ctx, p, tx2, &library.LibraryMutation{EventId: "second-progress", Operation: "book.progress", TargetId: id, ExpectedRevision: second.Revision, BookState: second})
		done <- e
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	waiting := false
	for !waiting {
		select {
		case e := <-done:
			t.Fatalf("second mutation did not wait for the first: %v", e)
		case <-deadline.C:
			t.Fatal("second mutation never reached its database lock")
		case <-tick.C:
			if err = pool.QueryRow(ctx, `SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, tx2.Conn().PgConn().PID()).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if !errors.Is(err, managedtracking.ErrConflict) {
			t.Fatalf("concurrent stale revision applied: %v", err)
		}
	case <-deadline.C:
		t.Fatal("second mutation did not finish")
	}

}
