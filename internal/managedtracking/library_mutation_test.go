package managedtracking

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
)

type libraryMutationFixture struct {
	*libraryFixtureSource
	writes int
	fail   bool
}

func (f *libraryMutationFixture) MutateLibrary(ctx context.Context, p Profile, tx pgx.Tx, in *library.LibraryMutation) (*library.LibraryMutationReceipt, error) {
	f.writes++
	if _, err := tx.Exec(ctx, `INSERT INTO bloem_companion_owned_copies(user_id,profile_id,id,payload) VALUES($1,$2,$3,'{}')`, p.AccountID, p.ProfileID, in.EventId); err != nil {
		return nil, err
	}
	if f.fail {
		return nil, ErrSnapshot
	}
	return &library.LibraryMutationReceipt{EventId: in.EventId, TargetId: in.EventId, Revision: 1, Status: "applied"}, nil
}
func TestLibraryMutationReceiptIsAtomicAndReplaySafe(t *testing.T) {
	f := enrolledFixture(t)
	source := &libraryMutationFixture{libraryFixtureSource: &libraryFixtureSource{calendarFixtureSource: &calendarFixtureSource{digest: "visible"}}}
	f.s.Calendar = source
	in := &library.LibraryMutation{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, EventId: "create-one", Operation: "collection.create", Name: "One"}
	out, err := f.s.MutateLibrary(t.Context(), f.id, in)
	if err != nil || out.TargetId != "create-one" {
		t.Fatal(out, err)
	}
	if _, err = f.s.MutateLibrary(t.Context(), f.id, in); err != nil || source.writes != 1 {
		t.Fatal("duplicate effect", source.writes, err)
	}
	in.Name = "Changed"
	if _, err = f.s.MutateLibrary(t.Context(), f.id, in); !errors.Is(err, ErrConflict) {
		t.Fatal("changed replay accepted", err)
	}
	in.EventId = "rolled-back"
	source.fail = true
	if _, err = f.s.MutateLibrary(t.Context(), f.id, in); !errors.Is(err, ErrSnapshot) {
		t.Fatal(err)
	}
	var count int
	if err = f.s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_companion_owned_copies WHERE user_id=$1 AND profile_id=$2 AND id='rolled-back'`, f.p.AccountID, f.p.ProfileID).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed effect persisted", count, err)
	}
}
