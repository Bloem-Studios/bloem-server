package scanner

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Silo-Server/silo-server/internal/rootcheck"
)

// RootSetObservation records the safety-relevant state of a configured root
// set. Callers must make every destructive reconciliation decision from this
// observation so all library scanners agree on what unreachable and
// unexpectedly empty mean.
type RootSetObservation struct {
	ConfiguredRoots   []string
	UnreachableRoots  []string
	EmptyRoots        []string
	SuspectEmptyRoots []string
}

// ObserveRoots applies the scanner's shared root-availability policy. A root
// is suspect-empty only when it is reachable and literally empty while the
// catalog still owns files beneath it; that ambiguity requires the explicit
// one-time empty-cleanup confirmation before destructive reconciliation.
func (s *Scanner) ObserveRoots(ctx context.Context, folderID int, roots []string) (RootSetObservation, error) {
	observation := RootSetObservation{ConfiguredRoots: cleanScanRoots(roots)}
	probes := rootcheck.ProbeManyWithTimeout(ctx, observation.ConfiguredRoots, rootcheck.DefaultProbeTimeout)
	observation.UnreachableRoots, observation.EmptyRoots = classifyRootProbeResults(observation.ConfiguredRoots, probes)

	for i, root := range observation.ConfiguredRoots {
		if i >= len(probes) || probes[i].Reachable {
			continue
		}
		logUnreachableRoot(ctx, folderID, root, probes[i])
	}
	if len(observation.EmptyRoots) == 0 || s == nil || s.fileRepo == nil {
		return observation, nil
	}

	suspect, err := s.fileRepo.ListRootsWithCatalogedFiles(ctx, folderID, observation.EmptyRoots)
	if err != nil {
		return RootSetObservation{}, fmt.Errorf("listing suspect-empty roots for folder %d: %w", folderID, err)
	}
	observation.SuspectEmptyRoots = suspect
	if len(suspect) > 0 {
		slog.WarnContext(ctx, "scanner: empty roots still hold cataloged files; protecting them from cleanup",
			"component", "scanner", "folder_id", folderID, "roots", suspect)
	}
	return observation, nil
}

func classifyRootProbeResults(roots []string, probes []rootcheck.Result) (unreachable, empty []string) {
	for i, root := range roots {
		probe := rootcheck.Result{}
		if i < len(probes) {
			probe = probes[i]
		}
		switch {
		case !probe.Reachable:
			unreachable = append(unreachable, root)
		case probe.Empty:
			empty = append(empty, root)
		}
	}
	return unreachable, empty
}
