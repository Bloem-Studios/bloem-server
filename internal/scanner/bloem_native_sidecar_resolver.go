package scanner

import (
	"context"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// NativeScanFolder reloads the persisted publisher input. The scan policy still
// authorizes the actual folder and locks it again through catalog publication.
func (s *Scanner) NativeScanFolder(ctx context.Context, id int) (*models.MediaFolder, error) {
	if s == nil || s.fileRepo == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	return catalog.NewFolderRepository(s.fileRepo.Pool()).GetByID(ctx, id)
}

type NativeSiblingCandidates interface {
	SiblingCandidates(context.Context, storagesource.IngestionLease, string, []string, []string) ([]*storagev1.Entry, bool, error)
}

// ResolveNativeEbookSidecars loads only the finite local-precedence candidates.
// The parent is a literal object-key prefix, preserving repeated slashes and ..;
// no logical path is evaluated by the host filesystem.
func ResolveNativeEbookSidecars(ctx context.Context, sources NativeSiblingCandidates, claim storagesource.IngestionClaim, open func(context.Context, storagesource.PersistedRef) (mediasource.File, error)) (NativeEbookSidecars, error) {
	var input NativeEbookSidecars
	if sources == nil || claim.Entry == nil {
		return input, storagesource.ErrReferenceConflict
	}
	book := claim.Entry
	parent, base := nativeSidecarParent(book.LogicalPath)
	if base != book.Name || base == "" {
		return input, storagesource.ErrReferenceConflict
	}
	stem := strings.ToLower(ebookTitleFromPath(book.Name))
	if stem == "" {
		return input, storagesource.ErrReferenceConflict
	}
	names := []string{stem + ".opf"}
	covers := []string{}
	for _, ext := range sidecarCoverExtensions {
		covers = append(covers, stem+ext)
	}
	specific := len(covers)
	for _, name := range sidecarCoverNames {
		for _, ext := range sidecarCoverExtensions {
			covers = append(covers, name+ext)
		}
	}
	seen := map[string]bool{names[0]: true}
	for _, name := range covers {
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	// SiblingCandidates indexes the final suffix. Include .zip conservatively
	// so unsupported .fb2.zip books never permit a multi-book generic cover.
	// An unrelated ZIP may suppress a generic cover, but cannot misassign one.
	suffixes := make([]string, 0, len(ebookExtensions)+1)
	suffixes = append(suffixes, ".zip")
	for suffix := range ebookExtensions {
		suffixes = append(suffixes, suffix)
	}
	entries, single, err := sources.SiblingCandidates(ctx, claim.Lease, parent, names, suffixes)
	if err != nil {
		return input, err
	}
	if len(entries) > len(names) {
		return input, fmt.Errorf("%w: sidecar result exceeds bound", storagesource.ErrCheckpointConflict)
	}
	byName := make(map[string]*storagev1.Entry, len(entries))
	for _, e := range entries {
		if e == nil {
			return input, storagesource.ErrCheckpointConflict
		}
		directory, base := nativeSidecarParent(e.LogicalPath)
		name := strings.ToLower(e.Name)
		if directory != parent || base != e.Name || !seen[name] || byName[name] != nil {
			return input, storagesource.ErrCheckpointConflict
		}
		byName[name] = e
	}
	input = NativeEbookSidecars{Complete: true, OPF: byName[names[0]], SingleEbookInDirectory: single, Open: open}
	if !single {
		covers = covers[:specific]
	}
	for _, name := range covers {
		if e := byName[name]; e != nil {
			input.Cover = e
			break
		}
	}
	return input, nil
}
func nativeSidecarParent(logical string) (string, string) {
	if i := strings.LastIndexByte(logical, '/'); i >= 0 {
		return logical[:i+1], logical[i+1:]
	}
	return "", logical
}
