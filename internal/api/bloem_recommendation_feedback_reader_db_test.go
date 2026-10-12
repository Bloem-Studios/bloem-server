//go:build integration

package api

import (
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/recommendations"
)

func TestManagedRecommendationChoicesReachBloemPersonalRows(t *testing.T) {
	pool := coverRecipeDatabase(t)
	var user, install int
	var tenant string
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM organizations WHERE is_default`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,password_hash,role,enabled) VALUES($1,'synthetic','user',true) RETURNING id`, "feedback-"+uuid.NewString()).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,enabled) VALUES('bloem.pastime','fixture','synthetic',true) RETURNING id`).Scan(&install); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO bloem_managed_grants(installation_id,tenant_id) VALUES($1,$2)`, install, tenant); err != nil {
		t.Fatal(err)
	}
	profile := uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO bloem_managed_recommendation_feedback(installation_id,user_id,profile_id,content_id,revision,dismissed) VALUES($1,$2,$3,'hidden',1,true)`, install, user, profile); err != nil {
		t.Fatal(err)
	}
	rows := []recommendations.ForYouRow{{Type: "cluster", Items: []recommendations.ScoredItem{{MediaItemID: "hidden"}, {MediaItemID: "keep"}}}, {Type: "popular", Items: []recommendations.ScoredItem{{MediaItemID: "hidden"}}}}
	reader := &managedRecommendationReader{pool: pool}
	out, err := reader.filter(t.Context(), user, profile, rows)
	if err != nil || len(out[0].Items) != 1 || out[0].Items[0].MediaItemID != "keep" || len(out[1].Items) != 1 {
		t.Fatal("personal exclusion or ordinary row", out, err)
	}
	if len(rows[0].Items) != 2 {
		t.Fatal("reader mutated shared cached data")
	}
	out, err = reader.filter(t.Context(), user, "other", rows)
	if err != nil || len(out[0].Items) != 2 {
		t.Fatal("other profile affected", out, err)
	}
	if _, err = pool.Exec(t.Context(), `UPDATE bloem_managed_recommendation_feedback SET dismissed=false WHERE installation_id=$1`, install); err != nil {
		t.Fatal(err)
	}
	out, err = reader.filter(t.Context(), user, profile, rows)
	if err != nil || len(out[0].Items) != 2 {
		t.Fatal("undo did not reach native rows", out, err)
	}
}
