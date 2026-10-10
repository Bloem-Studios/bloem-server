package bookrequests

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestEbookRequestSchemaDatabase(t *testing.T) {
	_, v, pool := databaseFixture(t)
	const insert = `INSERT INTO bloem_book_requests(id,organization_id,media_type,identity_key,title,isbn,asin,status,requested_by_user_id,requested_by_profile_id,client_key,request_digest) VALUES($1,$2,$3,$4,'Edition',$5,$6,'pending',$7,$8,$9,'fixture')`
	if _, err := pool.Exec(t.Context(), insert, uuid.New(), v.OrganizationID, "book", "isbn:9780306406157", "9780306406157", "", v.UserID, v.ProfileID, uuid.NewString()); err != nil {
		t.Fatalf("canonical ebook admission: %v", err)
	}
	for _, input := range []struct{ kind, identity, isbn, asin string }{
		{"book", "title:fixture", "", ""},
		{"book", "isbn:0306406152", "0306406152", ""},
		{"book", "title:fixture", "9780306406157", ""},
		{"book", "isbn:9780306406157", "9780306406157", "B012345678"},
		{"ebook", "isbn:9780306406157", "9780306406157", ""},
	} {
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), insert, uuid.New(), v.OrganizationID, input.kind, input.identity, input.isbn, input.asin, v.UserID, v.ProfileID, uuid.NewString())
		var constraint *pgconn.PgError
		if !errors.As(err, &constraint) || constraint.Code != "23514" {
			t.Errorf("invalid ebook identity did not fail a check: %+v %v", input, err)
		}
		_ = tx.Rollback(t.Context())
	}
	if _, err := pool.Exec(t.Context(), insert, uuid.New(), v.OrganizationID, "audiobook", "title:fixture", "", "", v.UserID, v.ProfileID, uuid.NewString()); err != nil {
		t.Fatalf("legacy title-only audiobook rejected: %v", err)
	}
}
