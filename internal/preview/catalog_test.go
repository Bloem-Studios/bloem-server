package preview_test

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/preview"
)

func TestPreviewAdminCatalogFiltersKeywordAndProviderLookup(t *testing.T) {
	ctx, pool := previewTestDB(t)
	actor := insertAccount(t, ctx, pool, "admin")
	a := preview.NewAdmin(pool, staticServerID("server-test"))
	id, err := a.EnsureAccount(ctx, actor, "operator-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=false,account_id=NULL; DELETE FROM users WHERE username='bloem-preview'; DELETE FROM media_items WHERE content_id LIKE 'picker-%'; DELETE FROM media_folders WHERE name LIKE 'picker-%'`)
	})
	var allowed, hidden int
	if err = pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies','picker-visible',true) RETURNING id`).Scan(&allowed); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies','picker-hidden',true) RETURNING id`).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id, rating   string
		age, library int
	}{{"picker-ok", "PG", 10, allowed}, {"picker-mature", "R", 17, allowed}, {"picker-private", "PG", 10, hidden}} {
		if _, err = pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title,status,genres,content_rating,content_rating_age,tmdb_id) VALUES($1,'movie','Picker Fixture','matched','{}',$2,$3,'12345')`, item.id, item.rating, item.age); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, item.id, item.library); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = a.SetLibraries(ctx, actor, "operator-test", []int{allowed}); err != nil {
		t.Fatal(err)
	}
	pub, _ := newKey(t)
	s := validSettings(t, id, pub)
	s.Issuer = "operator-test"
	s.MaturityCeiling = "PG-13"
	if err = a.Configure(ctx, actor, s, "lib.example.test"); err != nil {
		t.Fatal(err)
	}
	for _, in := range []struct{ query, provider, id string }{{"Picker Fixture", "", ""}, {"", "tmdb", "12345"}} {
		got, e := a.Catalog(ctx, actor, in.query, in.provider, in.id)
		if e != nil || len(got) != 1 || got[0].ContentID != "picker-ok" {
			var libs []int
			_ = pool.QueryRow(ctx, `SELECT g.library_ids FROM organization_memberships m JOIN access_groups g ON g.id=m.access_group_id WHERE m.account_id=$1`, id).Scan(&libs)
			t.Fatalf("picker scope (%+v), libs=%v: %+v %v", in, libs, got, e)
		}
	}
	if _, err = a.Catalog(ctx, id, "Picker Fixture", "", ""); err == nil {
		t.Fatal("shared account can use admin picker")
	}
	if _, err = pool.Exec(ctx, `UPDATE media_folders SET enabled=false WHERE id=$1`, allowed); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Catalog(ctx, actor, "", "tmdb", "12345"); err != nil || len(got) != 0 {
		t.Fatalf("disabled library exposed: %+v %v", got, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE media_folders SET enabled=true WHERE id=$1`, allowed); err != nil {
		t.Fatal(err)
	}
	if _, err = a.SetLibraries(ctx, actor, "operator-test", []int{}); err != nil {
		t.Fatal(err)
	}
	got, err := a.Catalog(ctx, actor, "", "tmdb", "12345")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty picker admits items: %+v %v", got, err)
	}
}
