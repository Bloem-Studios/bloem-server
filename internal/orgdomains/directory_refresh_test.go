package orgdomains

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// waitForRefresh waits until the background read that started has stored its
// map: the read holds d.mu from before it starts until after it stores.
func waitForRefresh(t *testing.T, d *Directory, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the background refresh never started")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cur.Load() == nil {
		t.Fatal("no map stored after the refresh")
	}
}

// An expired map is served while one background read replaces it, so a slow
// database never holds requests on every listener up at each refresh.
func TestDirectoryServesExpiredMapWhileRefreshing(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var loads atomic.Int32
	newer := testSnapshot()
	newer.hosts = map[string]uuid.UUID{"new.domain.com": testOrg2}
	d := newDirectory(func(context.Context) (snapshot, error) {
		if loads.Add(1) == 1 {
			return testSnapshot(), nil
		}
		started <- struct{}{}
		<-release
		return newer, nil
	}, 30*time.Second)
	var clock atomic.Int64
	d.now = func() time.Time { return time.Unix(0, clock.Load()) }

	if _, ok := d.Lookup(context.Background(), "main.domain.com"); !ok {
		t.Fatal("first load")
	}
	clock.Add(int64(31 * time.Second))

	done := make(chan bool, 1)
	go func() {
		_, ok := d.Lookup(context.Background(), "main.domain.com")
		done <- ok
	}()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("the expired map was not served")
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("a lookup waited for the refresh")
	}
	close(release)
	waitForRefresh(t, d, started)
	if _, ok := d.Lookup(context.Background(), "new.domain.com"); !ok {
		t.Fatal("the background refresh did not replace the map")
	}
	if n := loads.Load(); n != 2 {
		t.Fatalf("loads = %d, want one refresh", n)
	}
}

func TestDirectoryReloadsAfterTimeToLive(t *testing.T) {
	var clock atomic.Int64
	var loads atomic.Int32
	started := make(chan struct{}, 4)
	d := newDirectory(func(context.Context) (snapshot, error) {
		if loads.Add(1) > 1 {
			started <- struct{}{}
		}
		return testSnapshot(), nil
	}, 30*time.Second)
	d.now = func() time.Time { return time.Unix(0, clock.Load()) }
	d.Lookup(context.Background(), "x.example.com")
	d.Lookup(context.Background(), "x.example.com")
	if n := loads.Load(); n != 1 {
		t.Fatalf("loads within the time to live = %d, want 1", n)
	}
	clock.Add(int64(31 * time.Second))
	d.Lookup(context.Background(), "x.example.com")
	waitForRefresh(t, d, started)
	if n := loads.Load(); n != 2 {
		t.Fatalf("loads = %d, want a second after the time to live", n)
	}
}
