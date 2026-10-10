package bookrequests

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func insertEbookFixture(t *testing.T, pool *pgxpool.Pool, v Viewer) Request {
	t.Helper()
	r, err := scan(pool.QueryRow(t.Context(), `INSERT INTO bloem_book_requests(id,organization_id,media_type,identity_key,title,isbn,status,requested_by_user_id,requested_by_profile_id,client_key,request_digest) VALUES($1,$2,'book','isbn:9780306406157','Edition','9780306406157','pending',$3,$4,$5,'fixture') RETURNING `+selectColumns, uuid.New(), v.OrganizationID, v.UserID, v.ProfileID, uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEbookRowsStayOutOfAudiobookSurfaceDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	audio := submit(t, s, v, CreateInput{Title: "Audio edition", ISBN: "9780306406157"})
	book := insertEbookFixture(t, pool, v)
	for _, admin := range []bool{false, true} {
		viewer := v
		viewer.Admin = admin
		if _, err := s.Get(t.Context(), viewer, book.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("audiobook get exposed ebook (admin=%v): %v", admin, err)
		}
		rows, err := s.List(t.Context(), viewer, 100, 0)
		if err != nil || len(rows) != 1 || rows[0].ID != audio.ID {
			t.Errorf("audiobook list exposed ebook (admin=%v): %+v %v", admin, rows, err)
		}
		if _, err := s.Act(t.Context(), viewer, book.ID, "cancel", ActionInput{ExpectedRevision: book.Revision}); !errors.Is(err, ErrNotFound) {
			t.Errorf("audiobook action reached ebook (admin=%v): %v", admin, err)
		}
	}
	cap, err := s.Capability(t.Context(), v)
	if err != nil || cap.Used != 1 || len(cap.MediaTypes) != 1 || cap.MediaTypes[0] != "audiobook" {
		t.Errorf("ebook changed audiobook capability/count: %+v %v", cap, err)
	}
}

func TestEbookCannotCompleteFromAudiobookCatalogDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	audio := submit(t, s, v, CreateInput{Title: "Audio edition", ISBN: "9780306406157"})
	book := insertEbookFixture(t, pool, v)
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM resource_owners WHERE organization_id=$1`, v.OrganizationID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `INSERT INTO media_folders(id,name,type,owner_id) VALUES(919010,'Owned audio','audiobooks',$1)`, owner)
	bookSQL(t, pool, `INSERT INTO media_items(content_id,type,title) VALUES('audio-edition','audiobook','Edition')`)
	bookSQL(t, pool, `INSERT INTO media_item_provider_ids(content_id,item_type,provider,provider_id) VALUES('audio-edition','audiobook','isbn','9780306406157')`)
	bookSQL(t, pool, `INSERT INTO media_files(id,media_folder_id,file_path,content_id) VALUES(919010,919010,'/fixture/edition.m4b','audio-edition')`)
	if n, err := s.Reconcile(t.Context(), 100); err != nil || n != 1 {
		t.Errorf("reconciliation crossed types: completed=%d %v", n, err)
	}
	got, err := s.Get(t.Context(), v, audio.ID)
	if err != nil || got.Status != "completed" {
		t.Errorf("audiobook did not complete: %+v %v", got, err)
	}
	got, err = scan(pool.QueryRow(t.Context(), `SELECT `+selectColumns+` FROM bloem_book_requests WHERE id=$1`, book.ID))
	if err != nil || got.Status != "pending" || got.LibraryContentID != "" {
		t.Errorf("audiobook fulfilled ebook: %+v %v", got, err)
	}
	var checked bool
	if err := pool.QueryRow(t.Context(), `SELECT last_checked_at IS NOT NULL FROM bloem_book_requests WHERE id=$1`, book.ID).Scan(&checked); err != nil || checked {
		t.Errorf("audiobook reconciliation selected ebook: checked=%v %v", checked, err)
	}
}
