package managedtracking

import (
	"context"
	"errors"
	"testing"

	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
)

type libraryFixtureSource struct {
	*calendarFixtureSource
	calls int
	data  *LibraryData
}

func (f *libraryFixtureSource) LibraryAuthority(ctx context.Context, p Profile) (string, error) {
	return f.Authority(ctx, p)
}
func (f *libraryFixtureSource) ReadLibrary(ctx context.Context, p Profile, q LibraryQuery) (LibraryData, error) {
	f.calls++
	if f.afterRead != nil {
		f.afterRead()
	}
	if f.data != nil {
		return *f.data, nil
	}
	return LibraryData{Items: []*library.LibraryItem{{ContentId: "visible", Kind: "movie", Title: "Visible source title"}}, Next: "next"}, nil
}
func TestLibraryMetadataReadBindsCursorAndFreshAuthority(t *testing.T) {
	f := enrolledFixture(t)
	source := &libraryFixtureSource{calendarFixtureSource: &calendarFixtureSource{digest: "visible"}}
	f.s.Calendar = source
	q := &library.LibraryRequest{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, View: "catalog", Limit: 20, Query: "Visible"}
	out, err := f.s.ReadLibrary(t.Context(), f.id, q)
	if err != nil || len(out.Items) != 1 || out.NextCursor == "" || out.Complete {
		t.Fatal(out, err)
	}
	q.Cursor = out.NextCursor
	q.Query = "different"
	if _, err = f.s.ReadLibrary(t.Context(), f.id, q); !errors.Is(err, ErrSnapshot) {
		t.Fatal("cursor crossed query", err)
	}
	q.Query = "Visible"
	q.ProfileId = "foreign"
	if _, err = f.s.ReadLibrary(t.Context(), f.id, q); !errors.Is(err, ErrAuthority) {
		t.Fatal("cursor crossed profile", err)
	}
	q.ProfileId = f.p.ProfileID
	q.Cursor = ""
	source.afterRead = func() { source.digest = "changed" }
	if _, err = f.s.ReadLibrary(t.Context(), f.id, q); !errors.Is(err, ErrAuthority) {
		t.Fatal("changed source published", err)
	}
}

func TestLibraryMetadataRevocationDuringCallback(t *testing.T) {
	f := enrolledFixture(t)
	source := &libraryFixtureSource{calendarFixtureSource: &calendarFixtureSource{digest: "visible"}}
	source.afterRead = func() {
		if _, err := f.s.Pool.Exec(t.Context(), `UPDATE bloem_managed_grants SET active=false WHERE installation_id=$1`, f.id); err != nil {
			t.Fatal(err)
		}
	}
	f.s.Calendar = source
	if _, err := f.s.ReadLibrary(t.Context(), f.id, &library.LibraryRequest{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, View: "catalog", Limit: 20}); !errors.Is(err, ErrAuthority) {
		t.Fatal("revocation published", err)
	}
}

func TestLibraryMetadataRejectsMalformedRows(t *testing.T) {
	f := enrolledFixture(t)
	source := &libraryFixtureSource{calendarFixtureSource: &calendarFixtureSource{digest: "visible"}}
	f.s.Calendar = source
	for _, data := range []LibraryData{
		{Items: []*library.LibraryItem{nil}},
		{Items: []*library.LibraryItem{{ContentId: "a", Kind: "movie", Title: "A"}, {ContentId: "a", Kind: "movie", Title: "A"}}},
		{Items: []*library.LibraryItem{{ContentId: "a", Kind: "invalid", Title: "A"}}},
	} {
		source.data = &data
		if _, err := f.s.ReadLibrary(t.Context(), f.id, &library.LibraryRequest{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, View: "catalog", Limit: 20}); !errors.Is(err, ErrSnapshot) {
			t.Fatal("malformed source accepted", err)
		}
	}
}
