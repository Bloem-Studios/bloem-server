package preview_test

import (
	"math"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/preview"
)

// freePlayGrant opens a free-play guest session for grant and binds one
// playback session to it.
func freePlayGrant(t *testing.T, f *exchangeFixture, grant, playback string, allowance time.Duration) {
	t.Helper()
	f.setFreePlay(t, true)
	if _, err := f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: f.playTicket(t, grant, allowance, time.Now().Add(24*time.Hour)), Host: f.settings.Host, ClientIP: "198.51.100.40"}); err != nil {
		t.Fatal(err)
	}
	if err := preview.BindPlayback(f.ctx, f.pool, playback, grant); err != nil {
		t.Fatal(err)
	}
}

func usedSeconds(t *testing.T, f *exchangeFixture, grant string) float64 {
	t.Helper()
	var used float64
	if err := f.pool.QueryRow(f.ctx, `SELECT used_seconds FROM bloem_preview_play_grants WHERE grant_id=$1`, grant).Scan(&used); err != nil {
		t.Fatal(err)
	}
	return used
}

// lastDeliveryAgo moves the grant's last delivery into the past, standing in
// for the time a player spent between segment requests.
func lastDeliveryAgo(t *testing.T, f *exchangeFixture, grant string, ago time.Duration) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `UPDATE bloem_preview_play_grants SET last_delivery_at=now()-make_interval(secs => $2) WHERE grant_id=$1`, grant, ago.Seconds()); err != nil {
		t.Fatal(err)
	}
}

func charge(t *testing.T, f *exchangeFixture, playback string) bool {
	t.Helper()
	_, ok, err := preview.ChargeDelivery(f.ctx, f.pool, playback)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// The meter charges the time between segment deliveries, at most 20 seconds
// for one gap, so a pause stops it; the first delivery is free.
func TestChargeDeliveryMetersTheGapsBetweenSegments(t *testing.T) {
	f := newExchangeFixture(t, 200)
	grant, playback := "MeterMeterMeterMeterMe", "playback-meter-1"
	freePlayGrant(t, f, grant, playback, 30*time.Minute)
	if !charge(t, f, playback) || usedSeconds(t, f, grant) != 0 {
		t.Fatalf("first delivery charged %v", usedSeconds(t, f, grant))
	}
	lastDeliveryAgo(t, f, grant, 5*time.Second)
	if !charge(t, f, playback) {
		t.Fatal("refused with time left")
	}
	if got := usedSeconds(t, f, grant); math.Abs(got-5) > 0.5 {
		t.Fatalf("5 s gap charged %v", got)
	}
	lastDeliveryAgo(t, f, grant, 300*time.Second)
	charge(t, f, playback)
	if got := usedSeconds(t, f, grant); math.Abs(got-25) > 0.5 {
		t.Fatalf("a 300 s pause charged %v, want 20 more", got-5)
	}
}

// Two tabs on one grant fetch segments in turn; the grant pays wall-clock
// time once, not once per tab.
func TestChargeDeliveryChargesTwoTabsOnce(t *testing.T) {
	f := newExchangeFixture(t, 200)
	grant := "TabsTabsTabsTabsTabsTa"
	freePlayGrant(t, f, grant, "playback-tab-1", 30*time.Minute)
	if err := preview.BindPlayback(f.ctx, f.pool, "playback-tab-2", grant); err != nil {
		t.Fatal(err)
	}
	charge(t, f, "playback-tab-1")
	for i := range 20 {
		lastDeliveryAgo(t, f, grant, 3*time.Second)
		charge(t, f, []string{"playback-tab-1", "playback-tab-2"}[i%2])
	}
	if got := usedSeconds(t, f, grant); math.Abs(got-60) > 2 {
		t.Fatalf("two tabs over 60 s charged %v", got)
	}
}

// When the allowance is used, or free play is switched off, delivery is
// refused; a playback session no grant started never delivers.
func TestChargeDeliveryRefusesWhenSpentOffOrUnknown(t *testing.T) {
	f := newExchangeFixture(t, 200)
	grant, playback := "SpentSpentSpentSpentSp", "playback-spent-1"
	freePlayGrant(t, f, grant, playback, time.Minute)
	charge(t, f, playback)
	lastDeliveryAgo(t, f, grant, 20*time.Second)
	charge(t, f, playback)
	lastDeliveryAgo(t, f, grant, 20*time.Second)
	charge(t, f, playback)
	lastDeliveryAgo(t, f, grant, 20*time.Second)
	if charge(t, f, playback) {
		// 60 s of 60 used: this delivery still had time left when it began.
		lastDeliveryAgo(t, f, grant, 1*time.Second)
	}
	if charge(t, f, playback) {
		t.Fatalf("delivery after the allowance (used %v)", usedSeconds(t, f, grant))
	}
	if _, live, err := preview.LivePlayback(f.ctx, f.pool, playback); err != nil || live {
		t.Fatalf("spent playback live=%v err=%v", live, err)
	}
	other, otherPlayback := "OtherOtherOtherOtherOt", "playback-off-1"
	freePlayGrant(t, f, other, otherPlayback, 30*time.Minute)
	f.setFreePlay(t, false)
	if charge(t, f, otherPlayback) {
		t.Fatal("delivery with free play off")
	}
	f.setFreePlay(t, true)
	if charge(t, f, "playback-nobody-started") {
		t.Fatal("delivery for an unbound playback session")
	}
}

// Busy counts the other grants that had a segment delivered in the last
// minute; a grant's own streams never make it busy.
func TestFreePlayBusyCountsOtherRecentGrants(t *testing.T) {
	f := newExchangeFixture(t, 200)
	// Grants other tests charged share this database; only this test's count.
	if _, err := f.pool.Exec(f.ctx, `UPDATE bloem_preview_play_grants SET last_delivery_at=NULL`); err != nil {
		t.Fatal(err)
	}
	first, second := "BusyOneBusyOneBusyOneB", "BusyTwoBusyTwoBusyTwoB"
	freePlayGrant(t, f, first, "playback-busy-1", 30*time.Minute)
	freePlayGrant(t, f, second, "playback-busy-2", 30*time.Minute)
	charge(t, f, "playback-busy-1")
	busy := func(grant string, capacity int) bool {
		t.Helper()
		b, err := preview.FreePlayBusy(f.ctx, f.pool, grant, capacity)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if !busy(second, 1) {
		t.Fatal("one grant streaming, capacity 1: a second grant is not busy")
	}
	if busy(first, 1) {
		t.Fatal("a grant is busy because of its own stream")
	}
	if busy(second, 2) {
		t.Fatal("capacity 2 with one streaming is busy")
	}
	lastDeliveryAgo(t, f, first, 61*time.Second)
	if busy(second, 1) {
		t.Fatal("a grant idle for over a minute still counts")
	}
}
