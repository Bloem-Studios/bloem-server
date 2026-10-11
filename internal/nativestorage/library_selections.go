package nativestorage

import (
	"context"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const MaxSelectedRoots = 128
const MaxLibrarySources = 16

type RootSelection struct {
	SourceKey             uuid.UUID `json:"source_key"`
	ConfigurationRevision int64     `json:"configuration_revision"`
	SelectionRef          string    `json:"selection_ref"`
}
type LibrarySelection struct {
	Revision  int64                             `json:"revision"`
	EditToken string                            `json:"edit_token"`
	Paths     []string                          `json:"paths"`
	Locations []storagesource.LocationSelection `json:"locations"`
}
type preparedRoot struct {
	source                         storagesource.SourceConfig
	directoryID, name, logicalPath string
	generation                     uint64
}

// PreparedSelections can only be produced by PrepareSelections. Its evidence
// is bound to the exact actor and browser, expires, and cannot be copied into
// another object to fabricate preparation. No credentials are exposed.
type PreparedSelections struct {
	actor     auth.AdminContextClaims
	roots     []preparedRoot
	expiresAt time.Time
	browser   *SourceBrowser
	identity  *PreparedSelections
}

// SetSourceBrowser is startup-only; it must use the same host as delivery.
func (l *LibraryLocations) SetSourceBrowser(b *SourceBrowser) {
	if l != nil {
		l.browser = b
	}
}
func (l *LibraryLocations) PrepareSelections(ctx context.Context, a auth.AdminContextClaims, roots []RootSelection) (*PreparedSelections, error) {
	if l == nil || l.browser == nil {
		return nil, nativeDomainError("native_storage_unavailable")
	}
	sources := map[uuid.UUID]bool{}
	if len(roots) > MaxSelectedRoots {
		return nil, nativeDomainError("selection_limit_exceeded")
	}
	for _, r := range roots {
		sources[r.SourceKey] = true
	}
	if len(sources) > MaxLibrarySources {
		return nil, nativeDomainError("selection_limit_exceeded")
	}
	b := l.browser
	p := &PreparedSelections{actor: a, browser: b, expiresAt: a.ExpiresAt, roots: make([]preparedRoot, 0, len(roots))}
	p.identity = p
	seen := map[uuid.UUID]map[string]bool{}
	for _, r := range roots {
		if r.SourceKey == uuid.Nil || r.ConfigurationRevision <= 0 {
			return nil, nativeDomainError("invalid_request")
		}
		snapshot, err := b.snapshot(ctx, a, r.SourceKey)
		if err != nil {
			return nil, err
		}
		if snapshot.Source.ConfigurationRevision != r.ConfigurationRevision {
			return nil, nativeSourceRevision(snapshot.Source.ConfigurationRevision, r.ConfigurationRevision)
		}
		var ref browseReference
		if err := b.openBrowse(r.SelectionRef, selectionPurpose, &ref); err != nil {
			return nil, err
		}
		if err := b.validateReference(ref, a, snapshot.Source, snapshot.Generation); err != nil {
			return nil, err
		}
		node := ref.Ancestry[len(ref.Ancestry)-1]
		if seen[r.SourceKey] == nil {
			seen[r.SourceKey] = map[string]bool{}
		}
		if seen[r.SourceKey][node] {
			return nil, nativeDomainError("duplicate_selection")
		}
		seen[r.SourceKey][node] = true
		lease, ioCtx, cleanup, err := b.borrow(ctx, a, snapshot)
		if err != nil {
			return nil, err
		}
		_, err = directoryOnlyRequired(lease.Description, snapshot.Source.ProviderSourceID)
		var current *storagev1.Entry
		if err == nil {
			current, err = b.statDirectory(ioCtx, a, snapshot, lease, node)
		}
		if err == nil {
			err = ioCtx.Err()
		}
		cleanup()
		if err != nil {
			return nil, nativeDomainMap(err)
		}
		p.roots = append(p.roots, preparedRoot{source: snapshot.Source, directoryID: current.Id, name: current.Name, logicalPath: current.LogicalPath, generation: snapshot.Generation})
		if time.Unix(ref.ExpiresAt, 0).Before(p.expiresAt) {
			p.expiresAt = time.Unix(ref.ExpiresAt, 0)
		}
	}
	if !p.expiresAt.After(b.now()) {
		return nil, nativeDomainError("invalid_selection_reference")
	}
	return p, nil
}

// RetainPreparedTx rechecks private evidence after all commit lock waits. The
// returned graph includes removed locations so editors and scans share one
// order. It performs database checks only; provider I/O is never allowed here.
func (l *LibraryLocations) RetainPreparedTx(ctx context.Context, tx pgx.Tx, a auth.AdminContextClaims, folderID int, p *PreparedSelections) (resourcetenancy.NativeRetainedResources, error) {
	var out resourcetenancy.NativeRetainedResources
	if l == nil || l.browser == nil || p == nil || p.identity != p || p.browser != l.browser || !sameBrowseActor(a, p.actor) || !p.expiresAt.After(l.browser.now()) {
		return out, nativeDomainError("invalid_selection_reference")
	}
	set := resourcetenancy.NativeResourceSet{Library: &resourcetenancy.NativeLibraryExpectation{FolderID: folderID}}
	seen := map[uuid.UUID]bool{}
	for _, r := range p.roots {
		if !seen[r.source.Key] {
			set.Sources = append(set.Sources, resourcetenancy.NativeSourceExpectation{Source: r.source, Generation: r.generation})
			seen[r.source.Key] = true
		}
	}
	retained, err := l.resources.RetainNativeResourcesTx(ctx, tx, &a, set)
	if err != nil {
		return out, nativeDomainMap(err)
	}
	if !p.expiresAt.After(l.browser.now()) {
		return out, nativeDomainError("invalid_selection_reference")
	}
	return retained, nil
}
