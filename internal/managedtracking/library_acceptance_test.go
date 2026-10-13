package managedtracking

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
)

type realLibraryFixtureSource struct{ realCalendarFixtureSource }

func (realLibraryFixtureSource) LibraryAuthority(context.Context, Profile) (string, error) {
	return "library-fixture", nil
}
func (realLibraryFixtureSource) ReadLibrary(_ context.Context, p Profile, q LibraryQuery) (LibraryData, error) {
	if q.View == "collections" {
		return LibraryData{Collections: []*library.LibraryCollection{{Id: "personal:fixture", Name: "Synthetic collection", Scope: "personal", Writable: true, Revision: 1, DefinitionKind: "manual"}}}, nil
	}
	return LibraryData{Items: []*library.LibraryItem{{ContentId: "visible-" + p.ProfileID, Kind: "movie", Title: "Synthetic visible movie", ExternalIds: map[string]string{"tmdb": "42"}}}}, nil
}
func (realLibraryFixtureSource) MutateLibrary(ctx context.Context, p Profile, tx pgx.Tx, in *library.LibraryMutation) (*library.LibraryMutationReceipt, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO bloem_companion_owned_copies(user_id,profile_id,id,payload) VALUES($1,$2,$3,'{}')`, p.AccountID, p.ProfileID, in.EventId); err != nil {
		return nil, err
	}
	return &library.LibraryMutationReceipt{EventId: in.EventId, TargetId: in.TargetId, Revision: 2, Status: "applied"}, nil
}
func TestRealPluginLibraryBrowseAndCollectionWrite(t *testing.T) {
	pluginBinary, fixtureBinary := os.Getenv("BLOEM_MANAGED_PLUGIN_BINARY"), os.Getenv("BLOEM_MANAGED_PASTIME_FIXTURE_BINARY")
	if pluginBinary == "" || fixtureBinary == "" {
		t.Skip("explicit independently built plugin and Pastime fixture required")
	}
	s, id, p := enrollmentFixture(t)
	s.SetCalendarSource(realLibraryFixtureSource{})
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	run := startPastimeAcceptance(t, ctx, s, id, pluginBinary, fixtureBinary)
	run.start()
	run.reconcile()
	var request struct {
		RequestID string `json:"request_id"`
	}
	status, _ := run.call("POST", "/fixture/library-query", map[string]any{"Profile": p.ProfileID, "View": "collections"}, &request)
	if status != 200 || request.RequestID == "" {
		t.Fatal("queue", status, request)
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("library bridge did not converge")
			case <-ticker.C:
			}
		}
	}
	wait(func() bool {
		var out struct {
			Status string `json:"status"`
		}
		status, _ := run.call("GET", "/fixture/library-result?id="+request.RequestID, nil, &out)
		return status == 200 && out.Status == "ready"
	})
	var mutation struct {
		RequestID string `json:"request_id"`
	}
	status, _ = run.call("POST", "/fixture/library-change", map[string]any{"Profile": p.ProfileID, "Event": "rename-one"}, &mutation)
	if status != 200 || mutation.RequestID == "" {
		t.Fatal("mutation queue", status, mutation)
	}
	wait(func() bool {
		var out struct {
			Status   string `json:"status"`
			Revision int64  `json:"revision"`
		}
		status, _ := run.call("GET", "/fixture/library-change-result?id="+mutation.RequestID, nil, &out)
		return status == 200 && out.Status == "applied" && out.Revision == 2
	})
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM bloem_companion_owned_copies WHERE user_id=$1 AND profile_id=$2 AND id='rename-one'`, p.AccountID, p.ProfileID).Scan(&count); err != nil || count != 1 {
		t.Fatal("missing/duplicate native effect", count, err)
	}
	run.finish()
}

type realCopyBookFixtureSource struct{ realLibraryFixtureSource }

func (realCopyBookFixtureSource) ReadLibrary(_ context.Context, p Profile, q LibraryQuery) (LibraryData, error) {
	switch q.View {
	case "copies":
		return LibraryData{Copies: []*library.LibraryCopy{{Id: "copy-one", Kind: "ebook", Title: "Synthetic copy", Format: "hardcover", PriceCents: 1234, Currency: "EUR", Custom: map[string]string{"shelf": "A"}, Revision: 1}}}, nil
	case "book_states":
		return LibraryData{BookStates: []*library.BookState{{ContentId: "book-one", Kind: "ebook", Title: "Synthetic book", EditionId: "edition-one", FileId: 42, EbookLocation: "epubcfi(/6/4!/4/2:13)", EbookFraction: 0.123456789, ChangedAt: timestamppb.Now(), Revision: 1}}}, nil
	}
	return LibraryData{}, ErrSnapshot
}
func (realCopyBookFixtureSource) MutateLibrary(ctx context.Context, p Profile, tx pgx.Tx, in *library.LibraryMutation) (*library.LibraryMutationReceipt, error) {
	switch in.Operation {
	case "copy.upsert":
		if in.Copy == nil || in.Copy.Id != in.TargetId || in.Copy.PriceCents != 1234 || in.Copy.Custom["shelf"] != "A" {
			return nil, ErrSnapshot
		}
	case "book.progress":
		if in.BookState == nil || in.BookState.ContentId != in.TargetId || in.BookState.FileId != 42 || in.BookState.EditionId != "edition-one" || in.BookState.EbookFraction != 0.123456789 || in.BookState.EbookLocation != "epubcfi(/6/4!/4/2:13)" {
			return nil, ErrSnapshot
		}
	default:
		return nil, ErrSnapshot
	}
	return realLibraryFixtureSource{}.MutateLibrary(ctx, p, tx, in)
}
func TestRealPluginCopyAndBookPayloadRoundTrip(t *testing.T) {
	pluginBinary, fixtureBinary := os.Getenv("BLOEM_MANAGED_PLUGIN_BINARY"), os.Getenv("BLOEM_MANAGED_PASTIME_FIXTURE_BINARY")
	if pluginBinary == "" || fixtureBinary == "" {
		t.Skip("explicit independently built plugin and Pastime fixture required")
	}
	s, id, p := enrollmentFixture(t)
	s.SetCalendarSource(realCopyBookFixtureSource{})
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	run := startPastimeAcceptance(t, ctx, s, id, pluginBinary, fixtureBinary)
	run.start()
	run.reconcile()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("copy/book bridge did not converge")
			case <-ticker.C:
			}
		}
	}
	for _, view := range []string{"copies", "book_states"} {
		var request struct {
			RequestID string `json:"request_id"`
		}
		status, _ := run.call("POST", "/fixture/library-query", map[string]any{"Profile": p.ProfileID, "View": view}, &request)
		if status != 200 || request.RequestID == "" {
			t.Fatal("queue", status, request)
		}
		// The page also contains scalar fields, so decode its raw body separately.
		var response struct {
			Status string          `json:"status"`
			Page   json.RawMessage `json:"page"`
		}
		wait(func() bool {
			status, _ := run.call("GET", "/fixture/library-result?id="+request.RequestID, nil, &response)
			return status == 200 && response.Status == "ready"
		})
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(response.Page, &payload); err != nil {
			t.Fatal(err)
		}
		var records []map[string]any
		if err := json.Unmarshal(payload[view], &records); err != nil || len(records) != 1 {
			t.Fatal("payload", view, err)
		}
		record := records[0]
		intent := map[string]any{"expected_revision": 1}
		if view == "copies" {
			if record["price_cents"] != float64(1234) || record["custom"].(map[string]any)["shelf"] != "A" {
				t.Fatal("copy changed", record)
			}
			intent["operation"] = "copy.upsert"
			intent["target_id"] = "copy-one"
			intent["copy"] = record
		} else {
			if record["ebook_fraction"] != 0.123456789 || record["ebook_location"] != "epubcfi(/6/4!/4/2:13)" {
				t.Fatal("book changed", record)
			}
			intent["operation"] = "book.progress"
			intent["target_id"] = "book-one"
			intent["book_state"] = record
		}
		var mutation struct {
			RequestID string `json:"request_id"`
		}
		status, _ = run.call("POST", "/fixture/library-change", map[string]any{"Profile": p.ProfileID, "Event": "edit-" + view, "Intent": intent}, &mutation)
		if status != 200 || mutation.RequestID == "" {
			t.Fatal("mutation queue", status, mutation)
		}
		wait(func() bool {
			var out struct {
				Status   string `json:"status"`
				Revision int64  `json:"revision"`
			}
			status, _ := run.call("GET", "/fixture/library-change-result?id="+mutation.RequestID, nil, &out)
			return status == 200 && out.Status == "applied" && out.Revision == 2
		})
	}
	run.finish()
}
