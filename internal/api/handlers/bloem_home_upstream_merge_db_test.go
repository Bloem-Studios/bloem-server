package handlers

import (
	"context"
	"net/url"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

func TestBloemBundledHomeLayoutAppliesOwnedTransformerDB(t *testing.T) {
	f := newPagingIntegrationFixture(t)
	// Raw upstream paging accounts need a visible default membership before
	// Bloem's profile writer can resolve their tenant identity.
	f.exec(t, `INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		SELECT id, $1, 'active', 'user' FROM organizations WHERE is_default
		ON CONFLICT (account_id) DO NOTHING`, f.account)
	f.exec(t, `UPDATE users SET access_group_id = access_groups.id
		FROM organizations JOIN access_groups ON access_groups.organization_id = organizations.id
		WHERE organizations.is_default AND access_groups.is_default AND users.id = $1`, f.account)
	store, err := pgstore.NewPostgresProvider(f.pool).ForUser(t.Context(), f.account)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateProfile(t.Context(), userstore.Profile{ID: "owner", Name: "Owner"}); err != nil {
		t.Fatal(err)
	}
	h := NewSectionHandler(sections.NewRepository(f.pool), sections.NewFetcher(f.pool))
	h.StoreProvider = pagingIntegrationProvider{account: f.account, store: store}
	called := false
	h.HomeTransformers = []HomeSectionTransformer{func(_ context.Context, _ []sections.ResolvedSection, options url.Values) []sections.ResolvedSection {
		called = options.Get("promoted") == "1"
		return []sections.ResolvedSection{{ID: "merge-extension", SectionType: sections.SectionRecentlyAdded, Title: "Owned extension", ItemLimit: 2}}
	}}
	ctx := WithHomePromotions(apimw.SetProfileID(apimw.SetClaims(t.Context(), &auth.Claims{UserID: f.account}), "owner"), true)
	view, err := h.HomeLayoutWithItems(ctx, SectionViewer{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !called || len(view.Layout.Sections) != 1 || view.Layout.Sections[0].ID != "merge-extension" {
		t.Fatalf("called=%v layout=%+v", called, view.Layout)
	}
}
