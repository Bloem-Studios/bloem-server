package api

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

func TestPreviewScopeNarrowsCurrentVisibility(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	ctx := previewcontext.With(t.Context())
	var one, two int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies','preview-scope-one',true) RETURNING id`).Scan(&one); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies','preview-scope-two',true) RETURNING id`).Scan(&two); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE bloem_preview_settings SET maturity_ceiling='PG-13'`); err != nil {
		t.Fatal(err)
	}
	before := access.Scope{AllowedLibraryIDs: []int{one}, PlaybackAllowed: false, MaturityLimits: access.MaturityLimits{MaxContentRating: "PG"}}
	got, err := narrowBloemPreviewScope(ctx, pool, before)
	if err != nil || len(got.AllowedLibraryIDs) != 1 || got.AllowedLibraryIDs[0] != one || got.MaxContentRating != "PG" || !got.LibrariesRestricted {
		t.Fatalf("scope widened: %+v %v", got, err)
	}
	empty := before
	empty.AllowedLibraryIDs = []int{}
	got, err = narrowBloemPreviewScope(ctx, pool, empty)
	if err != nil || got.AllowedLibraryIDs == nil || len(got.AllowedLibraryIDs) != 0 {
		t.Fatalf("empty scope widened: %+v %v", got, err)
	}
	unrestricted := before
	unrestricted.MaxContentRating = ""
	got, err = narrowBloemPreviewScope(ctx, pool, unrestricted)
	if err != nil || got.MaxContentRating != "PG-13" {
		t.Fatalf("missing preview ceiling: %+v %v", got, err)
	}
	ordinary, err := narrowBloemPreviewScope(t.Context(), nil, access.Scope{PlaybackAllowed: true})
	if err != nil || !ordinary.PlaybackAllowed || ordinary.AllowedLibraryIDs != nil {
		t.Fatal("ordinary viewer queried or narrowed")
	}
	_ = two
}

// A free-play request may play; it still may not download or watch Live TV,
// and its libraries are the preview's. Without the mark nothing changes.
func TestPreviewScopeLetsAFreePlayRequestPlay(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	browse := previewcontext.With(t.Context())
	play := previewcontext.WithFreePlay(browse, "GrantGrantGrantGrantGr")
	before := access.Scope{PlaybackAllowed: true, DownloadAllowed: true, LiveTVAllowed: true}
	got, err := narrowBloemPreviewScope(play, pool, before)
	if err != nil || !got.PlaybackAllowed || got.DownloadAllowed || got.LiveTVAllowed || !got.LibrariesRestricted {
		t.Fatalf("free-play scope = %+v, %v", got, err)
	}
	got, err = narrowBloemPreviewScope(browse, pool, before)
	if err != nil || got.PlaybackAllowed {
		t.Fatalf("browse-only scope may play: %+v, %v", got, err)
	}
}
