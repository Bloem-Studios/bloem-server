package handlers

// Bloem-owned. The organization's viewing stats count as Silo's playback
// Insights does: the same plays, watch time and heatmap for the same people,
// and the same per-session client and adaptation-reason counts.

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/bloeminsights"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// bloemOrgStatsPool is Silo's Insights test schema (insightsTestPool) with the
// organization tables beside its tables.
func bloemOrgStatsPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := insightsTestPool(t)
	for _, table := range []string{"organizations", "organization_memberships"} {
		if _, err := pool.Exec(t.Context(), "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatalf("clone %s: %v", table, err)
		}
	}
	return pool
}

// seedStatsOrganization makes an organization whose active members are members.
func seedStatsOrganization(t *testing.T, pool *pgxpool.Pool, slug string, members ...int) uuid.UUID {
	t.Helper()
	var org uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations (slug, name, status, owner_account_id) VALUES ($1, $1, 'active', $2) RETURNING id`,
		slug, members[0]).Scan(&org); err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		if _, err := pool.Exec(t.Context(), `INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role) VALUES ($1, $2, 'active', 'user')`,
			org, member); err != nil {
			t.Fatal(err)
		}
	}
	return org
}

func TestBloemOrganizationStatsCountAsSiloInsightsDB(t *testing.T) {
	pool := bloemOrgStatsPool(t)
	s := seedPlayGrouping(t, pool)
	silo := &AdminHandler{pool: pool}
	store := bloeminsights.NewStore(pool)
	history := bloemSiloHistory()
	now := time.Now().UTC()
	for _, org := range []struct {
		slug    string
		members []int
	}{
		{"stats-org-a", []int{s.alice, s.bob, s.dave}},
		{"stats-org-b", []int{s.carol, s.erin}},
	} {
		scope := bloeminsights.Scope{OrganizationID: seedStatsOrganization(t, pool, org.slug, org.members...)}
		var wantHeat [7][24]int64
		var wantSessions int64
		want := map[int]AdminInsightsActivitySummary{}
		for _, member := range org.members {
			act, err := silo.ReadAdminInsightsActivity(t.Context(), playGroupingWeekFor(member), false)
			if err != nil {
				t.Fatal(err)
			}
			want[member] = act.Summary
			wantSessions += act.Summary.Sessions
			for _, cell := range act.Heatmap {
				wantHeat[cell.DayOfWeek][cell.Hour] += cell.Plays
			}
		}

		people, err := store.TopPeople(t.Context(), scope, 7, now, history)
		if err != nil {
			t.Fatal(err)
		}
		if len(people) != len(org.members) {
			t.Fatalf("%s: top people = %+v, want its %d members only", org.slug, people, len(org.members))
		}
		for _, p := range people {
			w, ok := want[p.UserID]
			if !ok {
				t.Fatalf("%s: %d is not a member", org.slug, p.UserID)
			}
			if p.Plays != w.Plays || math.Round(p.WatchedSeconds) != float64(w.WatchSeconds) {
				t.Errorf("%s user %d: plays %d, watched %.0f s; Silo's Insights: %d, %d s", org.slug, p.UserID, p.Plays, p.WatchedSeconds, w.Plays, w.WatchSeconds)
			}
		}

		heat, err := store.Heatmap(t.Context(), scope, 7, now, time.UTC, history)
		if err != nil {
			t.Fatal(err)
		}
		if heat != wantHeat {
			t.Errorf("%s: heatmap (Monday first) = %v, Silo's Insights = %v", org.slug, heat, wantHeat)
		}

		clients, err := store.Clients(t.Context(), scope, 7, now, history)
		if err != nil {
			t.Fatal(err)
		}
		var sessions, transcodes int64
		for _, c := range clients {
			sessions += c.Sessions
			transcodes += c.TranscodeSessions
		}
		wantTranscodes := int64(0)
		if org.slug == "stats-org-a" {
			wantTranscodes = 1 // dave's 40 s transcode
		}
		if sessions != wantSessions || transcodes != wantTranscodes {
			t.Errorf("%s: clients count %d sessions, %d transcoded; want %d, %d", org.slug, sessions, transcodes, wantSessions, wantTranscodes)
		}
	}
}

func TestBloemOrganizationClientsAndAdaptationReasonsDB(t *testing.T) {
	pool := bloemOrgStatsPool(t)
	ctx := t.Context()
	var anna, ben int
	for name, dest := range map[string]*int{"anna": &anna, "ben": &ben} {
		if err := pool.QueryRow(ctx, `INSERT INTO users (username, role) VALUES ($1, 'user') RETURNING id`, name).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	scope := bloeminsights.Scope{OrganizationID: seedStatsOrganization(t, pool, "reasons-org", anna)}
	seedStatsOrganization(t, pool, "reasons-other", ben)
	now := time.Now().UTC()
	insert := func(user int, client, platform, method string, reasons []string) {
		t.Helper()
		start := now.Add(-2 * time.Hour)
		if _, err := pool.Exec(ctx, `INSERT INTO admin_playback_history (session_id, user_id, profile_id, media_item_id, media_file_id, play_method,
			started_at, ended_at, watched_seconds, client_name, device_platform, adaptation_reasons)
			VALUES ($1, $2, 'p', 'M', 1, $3, $4, $4::timestamptz + interval '1 hour', 600, $5, $6, $7)`,
			"reasons-"+uuid.NewString(), user, method, start, client, platform, reasons); err != nil {
			t.Fatal(err)
		}
	}
	insert(anna, "Bloem TV", "android_tv", "transcode", []string{playback.AdaptationVideoCodecUnsupported})
	insert(anna, " bloem tv ", "Android_TV", "remux", []string{playback.AdaptationVideoCodecUnsupported, playback.AdaptationSubtitleBurnIn})
	insert(anna, "Bloem TV", "android_tv", "direct", []string{playback.AdaptationSubtitleBurnIn})
	insert(ben, "Bloem TV", "android_tv", "transcode", []string{playback.AdaptationVideoCodecUnsupported})
	history := bloemSiloHistory()

	clients, err := bloeminsights.NewStore(pool).Clients(ctx, scope, 7, now, history)
	if err != nil {
		t.Fatal(err)
	}
	// Which spelling names the merged client follows the database's collation, as in Silo's Devices tab.
	if len(clients) != 1 || !strings.EqualFold(clients[0].ClientName, "Bloem TV") || clients[0].Sessions != 3 || clients[0].TranscodeSessions != 1 {
		t.Fatalf("clients = %+v, want one Bloem TV with 3 sessions, 1 transcoded (names merged as Silo's Devices tab does)", clients)
	}

	causes, err := bloeminsights.NewStore(pool).TranscodeCauses(ctx, scope, 7, now, history)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, c := range causes {
		if !strings.EqualFold(c.ClientName, "Bloem TV") {
			t.Fatalf("cause client = %q, want Bloem TV", c.ClientName)
		}
		got[c.Reason] = c.Sessions
	}
	// Silo's Devices tab counts the reasons of every session not played directly: the transcode and the remux.
	if len(got) != 2 || got[playback.AdaptationVideoCodecUnsupported] != 2 || got[playback.AdaptationSubtitleBurnIn] != 1 {
		t.Fatalf("causes = %+v, want video_codec_unsupported 2 and subtitle_burn_in 1, from this organization's history only", causes)
	}
}
