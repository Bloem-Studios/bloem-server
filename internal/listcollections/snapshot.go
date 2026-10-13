package listcollections

import (
	"maps"
	"time"
)

type Snapshot struct {
	JobID, BindingID, SourceID, ID string
	Generation                     uint64
	Digest                         Hash
	GeneratedAt, ExpiresAt         time.Time
	Entries                        []Entry
}

// VerifySnapshot proves completeness before any domain effect. It owns its
// returned rows so later transport-buffer reuse cannot change verified data.
func VerifySnapshot(pages []Page, now time.Time) (Snapshot, error) {
	if len(pages) == 0 || len(pages) > MaxSnapshotItems {
		return Snapshot{}, ErrSnapshot
	}
	first := pages[0]
	out := Snapshot{JobID: first.JobID, BindingID: first.BindingID, SourceID: first.SourceID, ID: first.SnapshotID,
		Generation: first.Generation, Digest: first.Digest, GeneratedAt: first.GeneratedAt, ExpiresAt: first.ExpiresAt,
		Entries: []Entry{}}
	for i, p := range pages {
		if err := ValidatePageAt(p, now); err != nil {
			return Snapshot{}, err
		}
		if p.JobID != first.JobID || p.BindingID != first.BindingID || p.SourceID != first.SourceID ||
			p.SnapshotID != first.SnapshotID || p.Generation != first.Generation || p.LeaseToken != first.LeaseToken ||
			p.Digest != first.Digest || p.Total != first.Total || !p.GeneratedAt.Equal(first.GeneratedAt) ||
			!p.ExpiresAt.Equal(first.ExpiresAt) || p.Position != len(out.Entries) || p.Final != (i == len(pages)-1) {
			return Snapshot{}, ErrSnapshot
		}
		for _, e := range p.Entries {
			e.ExternalIDs = maps.Clone(e.ExternalIDs)
			out.Entries = append(out.Entries, e)
		}
	}
	if len(out.Entries) != first.Total {
		return Snapshot{}, ErrSnapshot
	}
	digest, err := Digest(out.Entries)
	if err != nil || digest != first.Digest {
		return Snapshot{}, ErrSnapshot
	}
	return out, nil
}
