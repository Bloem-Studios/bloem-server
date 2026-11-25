package bloeminsights

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/bloemgeo"
)

func seedInsightsUser(t *testing.T, pool *pgxpool.Pool, id int, name string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, username, email, password_hash, role) VALUES ($1, $2, $3, '', 'user')
		ON CONFLICT (id) DO NOTHING`, id, name, name+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_playback_history WHERE user_id = $1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
}

func liveStart(session string, userID string, at time.Time) LifecycleEvent {
	ev := startedEvent(session, 1)
	ev.At = at
	ev.Session.UserID = userID
	ev.Session.StartedAt = at
	return ev
}

func TestTrailsFollowEveryHopWithCoordinates(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	geo := &bloemgeo.Location{City: "Leiden", CountryCode: "NL", Country: "Netherlands", Latitude: 52.16, Longitude: 4.49}
	full := &FactsSummary{
		Latest: RequestFacts{Proto: "HTTP/1.1", CFColo: "AMS", Edge: &EdgeHop{Name: "ams-1", Latitude: 52.37, Longitude: 4.89, ViewerProto: "cloudflare"}},
		Counts: map[Transport]int64{TransportCloudflare: 4},
	}
	if err := store.ApplyLifecycle(ctx, liveStart("trail-full", "7", now.Add(-time.Minute)), geo, full); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyLifecycle(ctx, liveStart("trail-plain", "7", now.Add(-2*time.Minute)), geo, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyLifecycle(ctx, liveStart("trail-old", "7", now.Add(-3*24*time.Hour)), geo, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyLifecycle(ctx, stoppedEvent("trail-old", 2, now.Add(-3*24*time.Hour+time.Hour)), nil, nil); err != nil {
		t.Fatal(err)
	}
	positions := Positions{Server: &Hop{Kind: HopServer, Label: "Home", Latitude: 52.0, Longitude: 5.0}, Nodes: map[int]Hop{4: {Kind: HopNode, Label: "a310", Latitude: 51.9, Longitude: 4.4}}}

	trails, truncated, err := store.Trails(ctx, WindowLive, now, positions, 2000)
	if err != nil || truncated {
		t.Fatalf("live trails err = %v truncated = %v", err, truncated)
	}
	if len(trails) != 2 || trails[0].SessionID != "trail-full" || trails[1].SessionID != "trail-plain" {
		t.Fatalf("live trails = %+v, want the two live streams newest first", trails)
	}
	kinds := func(tr Trail) []string {
		var out []string
		for _, h := range tr.Hops {
			out = append(out, h.Kind)
		}
		return out
	}
	if got := kinds(trails[0]); len(got) != 5 || got[0] != HopViewer || got[1] != HopCloudflare || got[2] != HopEdge || got[3] != HopServer || got[4] != HopNode {
		t.Fatalf("full trail hops = %v, want viewer, cloudflare, edge, server, node", got)
	}
	if trails[0].Transport != TransportCloudflare || !trails[0].Live {
		t.Fatalf("full trail = %+v", trails[0])
	}
	if got := kinds(trails[1]); len(got) != 3 || got[0] != HopViewer || got[1] != HopServer || got[2] != HopNode {
		t.Fatalf("plain trail hops = %v, want viewer, server, node", got)
	}
	week, _, err := store.Trails(ctx, Window7d, now, Positions{}, 2000)
	if err != nil || len(week) != 3 {
		t.Fatalf("7-day trails = %d (%v), want 3", len(week), err)
	}
	if got := kinds(week[2]); len(got) != 1 || got[0] != HopViewer {
		t.Fatalf("trail without positions = %v, want the viewer only", got)
	}
	one, truncated, err := store.Trails(ctx, Window7d, now, Positions{}, 1)
	if err != nil || len(one) != 1 || !truncated {
		t.Fatalf("capped trails = %d truncated = %v err = %v", len(one), truncated, err)
	}
}

func TestTransportCountsAndSeries(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour).Add(30 * time.Minute)
	udp := &FactsSummary{Latest: RequestFacts{Proto: "HTTP/3.0"}, Counts: map[Transport]int64{TransportUDP: 3}}
	tcp := &FactsSummary{Latest: RequestFacts{Proto: "HTTP/2.0"}, Counts: map[Transport]int64{TransportTCP: 3}}
	if err := store.ApplyLifecycle(ctx, liveStart("tr-a", "7", now.Add(-10*time.Minute)), nil, udp); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyLifecycle(ctx, liveStart("tr-b", "7", now.Add(-3*time.Hour)), nil, tcp); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyLifecycle(ctx, stoppedEvent("tr-b", 2, now.Add(-2*time.Hour)), nil, tcp); err != nil {
		t.Fatal(err)
	}
	live, err := store.Transport(ctx, WindowLive, now)
	if err != nil || live.Counts[TransportUDP] != 1 || live.Counts[TransportTCP] != 0 {
		t.Fatalf("live counts = %+v err = %v", live.Counts, err)
	}
	day, err := store.Transport(ctx, Window24h, now)
	if err != nil || day.Counts[TransportUDP] != 1 || day.Counts[TransportTCP] != 1 {
		t.Fatalf("24h counts = %+v err = %v", day.Counts, err)
	}
	if len(day.Series) != 24 {
		t.Fatalf("24h series = %d buckets, want 24", len(day.Series))
	}
	var udpTotal, tcpTotal int64
	for _, b := range day.Series {
		udpTotal += b.UDP
		tcpTotal += b.TCP
	}
	if udpTotal != 1 || tcpTotal != 1 {
		t.Fatalf("series totals udp %d tcp %d, want 1 and 1", udpTotal, tcpTotal)
	}
	week, err := store.Transport(ctx, Window7d, now)
	if err != nil || len(week.Series) != 28 {
		t.Fatalf("7d series = %d buckets (%v), want 28", len(week.Series), err)
	}
}

func TestRefusalPages(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	seedInsightsUser(t, pool, 9201, "refused-anna")
	now := time.Now().UTC()
	for i := range 5 {
		if _, err := pool.Exec(ctx, `INSERT INTO bloem_limit_refusals (user_id, kind, scope, refused_at) VALUES (9201, 'streams', 'person', $1)`,
			now.Add(-time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bloem_limit_refusals (user_id, kind, scope, refused_at) VALUES (9201, 'transcodes', 'household', $1)`,
		now.Add(-10*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	page, next, err := store.Refusals(ctx, Scope{}, 7, now, "", 3)
	if err != nil || len(page) != 3 || next == "" || page[0].Username != "refused-anna" || page[0].Kind != "streams" {
		t.Fatalf("first page = %+v next = %q err = %v", page, next, err)
	}
	rest, next, err := store.Refusals(ctx, Scope{}, 7, now, next, 3)
	if err != nil || len(rest) != 2 || next != "" {
		t.Fatalf("second page = %+v next = %q err = %v", rest, next, err)
	}
	if !rest[0].RefusedAt.Before(page[2].RefusedAt) {
		t.Fatal("pages overlap or run backwards")
	}
	if _, _, err := store.Refusals(ctx, Scope{}, 7, now, "not-a-cursor", 3); err == nil {
		t.Fatal("a bad cursor was accepted")
	}
}

func TestParseWindowAndDays(t *testing.T) {
	for _, ok := range []string{"live", "24h", "7d"} {
		if _, err := ParseWindow(ok); err != nil {
			t.Fatalf("window %s refused", ok)
		}
	}
	if _, err := ParseWindow("30d"); err == nil {
		t.Fatal("window 30d accepted")
	}
	for raw, want := range map[string]int{"": 30, "1": 1, "90": 90} {
		if got, err := ParseDays(raw, 30); err != nil || got != want {
			t.Fatalf("days %q = %d %v, want %d", raw, got, err, want)
		}
	}
	for _, bad := range []string{"0", "91", "-1", "x"} {
		if _, err := ParseDays(bad, 30); err == nil {
			t.Fatalf("days %q accepted", bad)
		}
	}
}
