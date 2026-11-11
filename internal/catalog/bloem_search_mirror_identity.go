package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
)

// Identity markers tell the two Meilisearch servers apart. Both Silo copies reach their
// servers at the same address, so only what a server carries can say which one it is: a small
// index, "<prefix>_bloem_target", holds one random id. The mirror's state row remembers which
// id is the main server's and which is the mirror's, and every run compares them with what
// the servers show (spec §7).
//
// The marker is the only thing the mirror ever writes to the main server.

// errSearchMirrorSameServer is what settleTargets returns once the markers have shown that the
// mirror address reaches the main server. The mirror is then misconfigured/same_server and
// this process makes no further writes: the caller must stop the run and leave that state
// alone.
var errSearchMirrorSameServer = errors.New("the mirror points at the main search server")

// searchMirrorSameServerLatch holds the mirrors that proved their address reaches the main
// server. The latch lasts as long as the process: a restart proves it again, so an operator who
// corrects the address needs no other reset. It is keyed by mirror because SearchMirror is
// defined with the mirror run, not here.
var searchMirrorSameServerLatch sync.Map // *SearchMirror -> struct{}

// searchMirrorTargets is what a run saw on each server: the identity it carries, or the error
// that stopped the read. A server with no marker reads as "" and no error.
type searchMirrorTargets struct {
	Main, Mirror       string
	MainErr, MirrorErr error
}

// searchMirrorVerdict says how the servers compare with what the mirror remembers.
type searchMirrorVerdict string

const (
	verdictNormal     searchMirrorVerdict = "normal"      // both servers are as remembered
	verdictSwapped    searchMirrorVerdict = "swapped"     // the roles changed: a failover
	verdictClone      searchMirrorVerdict = "clone"       // the mirror server is a copy of the main
	verdictNewMirror  searchMirrorVerdict = "new_mirror"  // the mirror server is new or was replaced
	verdictNewMain    searchMirrorVerdict = "new_main"    // the main server was replaced
	verdictMirrorDown searchMirrorVerdict = "mirror_down" // the mirror's marker can't be read
	verdictFresh      searchMirrorVerdict = "fresh"       // nothing is remembered yet
)

// judgeSearchMirrorTargets compares the markers seen with the remembered ids (§7's table). The
// rules apply in order, and a remembered or seen id of "" never equals anything:
//  1. The mirror's marker can't be read: mirror_down. Nothing is applied.
//  2. Nothing remembered: fresh.
//  3. Both markers set and equal: clone.
//  4. The main shows the remembered mirror's id, or the mirror shows the remembered main's, and
//     neither server shows its own remembered id: swapped.
//  5. The mirror has no marker or an unknown one: new_mirror.
//  6. The main has no marker or an unknown one: new_main.
//  7. Otherwise: normal.
//
// A main whose marker can't be read is not a main with no marker, and says nothing about the
// rules that need it (3, 4, 6): it does not block a mirror whose own marker matches.
func judgeSearchMirrorTargets(rememberedMain, rememberedMirror string, seen searchMirrorTargets) searchMirrorVerdict {
	if seen.MirrorErr != nil {
		return verdictMirrorDown
	}
	if rememberedMain == "" && rememberedMirror == "" {
		return verdictFresh
	}
	mainRead := seen.MainErr == nil
	if mainRead && seen.Main != "" && seen.Main == seen.Mirror {
		return verdictClone
	}
	if mainRead {
		mainShowsMirror := rememberedMirror != "" && seen.Main == rememberedMirror
		mirrorShowsMain := rememberedMain != "" && seen.Mirror == rememberedMain
		mainShowsItself := rememberedMain != "" && seen.Main == rememberedMain
		mirrorShowsItself := rememberedMirror != "" && seen.Mirror == rememberedMirror
		if (mainShowsMirror || mirrorShowsMain) && !mainShowsItself && !mirrorShowsItself {
			return verdictSwapped
		}
	}
	if seen.Mirror == "" || seen.Mirror != rememberedMirror {
		return verdictNewMirror
	}
	if mainRead && (seen.Main == "" || seen.Main != rememberedMain) {
		return verdictNewMain
	}
	return verdictNormal
}

// settleTargets reads both markers, judges them against the ids remembered in row and acts on
// the verdict:
//   - fresh: adopts both markers when they differ and are set; otherwise writes new ids, the
//     mirror's first. Then it sets ForceCopy.
//   - clone, new_mirror: writes a new id to the mirror and sets ForceCopy.
//   - new_main: writes a new id to the main, once. The main index rebuilds by itself and the
//     mirror carries on.
//   - normal: nothing.
//   - swapped: nothing here. The caller runs the swap step.
//   - mirror_down: nothing is written anywhere. The error is the mirror's marker error.
//
// Every id written to a server is saved in row at once, with ForceCopy where it is set, so a
// crash after a write never loses the copy it called for. Each id written to one server is
// followed by a read of the other, to prove the two addresses are different servers: when they
// are not, the row becomes misconfigured/same_server, errSearchMirrorSameServer is returned
// and this process writes nothing more.
//
// A verdict that needs the main's identity (fresh, new_mirror) is not acted on while the main's
// marker can't be read: the error comes back and nothing is written.
func (m *SearchMirror) settleTargets(ctx context.Context, row *searchMirrorRow) (searchMirrorVerdict, error) {
	if _, latched := searchMirrorSameServerLatch.Load(m); latched {
		return "", m.keepSameServerState(ctx, row)
	}

	prefix := m.startup.MeilisearchIndex
	var seen searchMirrorTargets
	seen.Mirror, seen.MirrorErr = m.mirror.readTargetMarker(ctx, prefix)
	if seen.MirrorErr != nil {
		// The mirror's own marker can't be confirmed, so nothing may be applied to it. The
		// main's marker would not change that.
		return verdictMirrorDown, fmt.Errorf("read the mirror's identity marker: %w", seen.MirrorErr)
	}
	seen.Main, seen.MainErr = m.main.readTargetMarker(ctx, prefix)

	verdict := judgeSearchMirrorTargets(row.State.MainTarget, row.State.MirrorTarget, seen)
	switch verdict {
	case verdictNormal, verdictSwapped:
		return verdict, nil
	case verdictFresh, verdictNewMirror:
		if seen.MainErr != nil {
			return "", fmt.Errorf("read the main's identity marker: %w", seen.MainErr)
		}
	}

	switch verdict {
	case verdictFresh:
		if seen.Main != "" && seen.Mirror != "" && seen.Main != seen.Mirror {
			row.State.MainTarget, row.State.MirrorTarget = seen.Main, seen.Mirror
		} else if err := m.writeNewTargets(ctx, row); err != nil {
			return "", err
		}
		row.State.ForceCopy = true
	case verdictClone:
		if err := m.writeNewMirrorTarget(ctx, row); err != nil {
			return "", err
		}
		row.State.MainTarget = seen.Main
		row.State.ForceCopy = true
	case verdictNewMirror:
		if err := m.writeNewMirrorTarget(ctx, row); err != nil {
			return "", err
		}
		row.State.ForceCopy = true
	case verdictNewMain:
		if err := m.writeNewMainTarget(ctx, row); err != nil {
			return "", err
		}
	}

	// A same-server finding from an earlier run is over: the servers have just been told apart.
	if row.State.State == SearchMirrorMisconfigured && row.State.Reason == string(SearchMirrorProblemSameServer) {
		row.State.State, row.State.Reason = "", ""
	}
	if err := saveSearchMirrorRow(ctx, m.pool, *row); err != nil {
		return "", searchMirrorDatabaseError(ctx, err)
	}
	return verdict, nil
}

// writeNewTargets gives both servers a new id, the mirror's first, and remembers them in row.
func (m *SearchMirror) writeNewTargets(ctx context.Context, row *searchMirrorRow) error {
	if err := m.writeNewMirrorTarget(ctx, row); err != nil {
		return err
	}
	return m.writeNewMainTarget(ctx, row)
}

// writeNewMirrorTarget writes a new id to the mirror and remembers it in row. Then it reads the
// main's marker: if the main shows the same id, the mirror address is the main server.
func (m *SearchMirror) writeNewMirrorTarget(ctx context.Context, row *searchMirrorRow) error {
	prefix := m.startup.MeilisearchIndex
	id := uuid.NewString()
	if err := m.mirror.writeTargetMarker(ctx, prefix, id); err != nil {
		return fmt.Errorf("write the mirror's identity marker: %w", err)
	}
	onMain, err := m.main.readTargetMarker(ctx, prefix)
	if err != nil {
		return fmt.Errorf("check the mirror is not the main server: read the main's identity marker: %w", err)
	}
	if onMain == id {
		return m.refuseSameServer(ctx, row)
	}
	row.State.MirrorTarget = id
	return nil
}

// writeNewMainTarget writes a new id to the main and remembers it in row. Then it reads the
// mirror's marker: if the mirror shows the same id, the two addresses are one server.
func (m *SearchMirror) writeNewMainTarget(ctx context.Context, row *searchMirrorRow) error {
	prefix := m.startup.MeilisearchIndex
	id := uuid.NewString()
	if err := m.main.writeTargetMarker(ctx, prefix, id); err != nil {
		return fmt.Errorf("write the main's identity marker: %w", err)
	}
	onMirror, err := m.mirror.readTargetMarker(ctx, prefix)
	if err != nil {
		return fmt.Errorf("check the mirror is not the main server: read the mirror's identity marker: %w", err)
	}
	if onMirror == id {
		return m.refuseSameServer(ctx, row)
	}
	row.State.MainTarget = id
	return nil
}

// refuseSameServer latches the finding for this process, logs it once, and saves
// misconfigured/same_server. The ids written to find it are not remembered: they describe one
// server, not two.
func (m *SearchMirror) refuseSameServer(ctx context.Context, row *searchMirrorRow) error {
	if _, loaded := searchMirrorSameServerLatch.LoadOrStore(m, struct{}{}); !loaded {
		slog.Error("search mirror: the mirror address reaches the main search server; mirroring is off until this process restarts")
	}
	return m.keepSameServerState(ctx, row)
}

// keepSameServerState makes row misconfigured/same_server, saving it when the database does
// not say so already, and returns errSearchMirrorSameServer. It never touches a server.
func (m *SearchMirror) keepSameServerState(ctx context.Context, row *searchMirrorRow) error {
	reason := string(SearchMirrorProblemSameServer)
	if row.State.State == SearchMirrorMisconfigured && row.State.Reason == reason {
		return errSearchMirrorSameServer
	}
	row.State.State = SearchMirrorMisconfigured
	row.State.Reason = reason
	if err := saveSearchMirrorRow(ctx, m.pool, *row); err != nil {
		return errors.Join(errSearchMirrorSameServer, err)
	}
	return errSearchMirrorSameServer
}
