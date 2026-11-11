package catalog

import (
	"slices"
	"testing"
)

// assertSearchMirrorIndexes fails the test unless server holds exactly want.
func assertSearchMirrorIndexes(t *testing.T, server *fakeMeili, label string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if got := server.Indexes(); !slices.Equal(got, want) {
		t.Errorf("%s indexes = %q, want %q", label, got, want)
	}
}

// Only this prefix's copy side indexes go, and never the main's active index: the marker,
// Silo's rebuild names, other applications' indexes (another prefix's copy included) and the
// mirror server are untouched.
func TestSearchMirrorCleanMainCopyLeftoversDeletesOnlyItsOwnSideIndexes(t *testing.T) {
	e := newSearchMirrorTestEnv(t)
	active := e.Prefix + "_rebuild_9"
	e.SetMainState(t, active)
	leftover := e.Prefix + "_mirrorcopy_1"
	marker := searchMirrorMarkerIndex(e.Prefix)
	for _, uid := range []string{leftover, marker, "other_app", "other_app_mirrorcopy_1"} {
		e.Main.CreateIndexDirect(uid)
	}
	mirrorSide := e.Prefix + "_mirrorcopy_2"
	e.Mirror.CreateIndexDirect(mirrorSide)

	m := e.NewMirror(t)
	if err := m.cleanMainCopyLeftovers(t.Context()); err != nil {
		t.Fatalf("cleanMainCopyLeftovers: %v", err)
	}
	assertSearchMirrorIndexes(t, e.Main, "main", active, marker, "other_app", "other_app_mirrorcopy_1")
	assertSearchMirrorIndexes(t, e.Mirror, "mirror", mirrorSide)

	// Nothing left to do is not an error.
	if err := m.cleanMainCopyLeftovers(t.Context()); err != nil {
		t.Fatalf("cleanMainCopyLeftovers, nothing left: %v", err)
	}
}

// A main whose active index has a copy side index's name keeps it.
func TestSearchMirrorCleanMainCopyLeftoversKeepsAnActiveCopyName(t *testing.T) {
	e := newSearchMirrorTestEnv(t)
	active := e.Prefix + "_mirrorcopy_5"
	e.SetMainState(t, active)
	leftover := e.Prefix + "_mirrorcopy_1"
	e.Main.CreateIndexDirect(leftover)

	if err := e.NewMirror(t).cleanMainCopyLeftovers(t.Context()); err != nil {
		t.Fatalf("cleanMainCopyLeftovers: %v", err)
	}
	assertSearchMirrorIndexes(t, e.Main, "main", active)
}

// A run with both servers as remembered looks once per process; the swap after a failover
// cleans the new main (the old mirror server), where a copy cut short left its side index.
func TestSearchMirrorRunsCleanMainCopyLeftovers(t *testing.T) {
	e := newSearchMirrorTestEnv(t)
	active := e.Prefix + "_rebuild_9"
	e.SetMainState(t, active)
	e.Docs.Put("m1", "One")
	marker := searchMirrorMarkerIndex(e.Prefix)

	run := func(m *SearchMirror, label string) {
		t.Helper()
		if err := m.RunOnce(t.Context(), nil); err != nil {
			t.Fatalf("%s: RunOnce: %v", label, err)
		}
	}

	// The first run writes the markers and publishes a full copy: its verdict is fresh.
	a := e.NewMirror(t)
	run(a, "first run")
	e.AssertMatchesDatabase(t, e.Mirror, active)

	e.Main.CreateIndexDirect(e.Prefix + "_mirrorcopy_1")
	run(a, "first normal run")
	assertSearchMirrorIndexes(t, e.Main, "main after the first normal run", active, marker)

	later := e.Prefix + "_mirrorcopy_2"
	e.Main.CreateIndexDirect(later)
	run(a, "second normal run")
	assertSearchMirrorIndexes(t, e.Main, "main after the second normal run", active, marker, later)

	// Another process looks again at its start.
	run(e.NewMirror(t), "new process")
	assertSearchMirrorIndexes(t, e.Main, "main after a new process's run", active, marker)

	// A failover during a copy: the old mirror server becomes the main and still holds the
	// copy's side index. The process on the new copy sees the servers the other way round.
	side := e.Prefix + "_mirrorcopy_77"
	e.Mirror.CreateIndexDirect(side)
	onOldMain := e.Prefix + "_mirrorcopy_88"
	e.Main.CreateIndexDirect(onOldMain)
	b := e.NewMirror(t)
	b.main, b.mirror = b.mirror, b.main
	run(b, "swap run")

	row, _, err := loadSearchMirrorRow(t.Context(), e.Pool)
	if err != nil {
		t.Fatalf("load the mirror row: %v", err)
	}
	if row.State.RoleSwaps != 1 || row.State.Swap == nil {
		t.Fatalf("role swaps = %d, swap = %+v; want one swap", row.State.RoleSwaps, row.State.Swap)
	}
	assertSearchMirrorIndexes(t, e.Mirror, "new main after the swap", active, marker)
	// The new mirror (the old main) is not the swap's to clean.
	assertSearchMirrorIndexes(t, e.Main, "new mirror after the swap", active, marker, onOldMain)
}
