package nativestorage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type BrowseRequest struct {
	DirectoryRef string `json:"directory_ref"`
	Cursor       string `json:"cursor"`
	Limit        int    `json:"limit"`
}
type BrowseDirectory struct {
	SelectionRef string `json:"selection_ref"`
	Name         string `json:"name"`
	LogicalPath  string `json:"logical_path"`
}
type BrowsePage struct {
	Current     BrowseDirectory   `json:"current"`
	Breadcrumbs []BrowseDirectory `json:"breadcrumbs"`
	Directories []BrowseDirectory `json:"directories"`
	NextCursor  string            `json:"next_cursor"`
}

// SourceBrowser uses the retained host registry and runtime. It has no scan
// queue, file reader, URL resolver or configuration mutation capability.
type SourceBrowser struct {
	pool        *pgxpool.Pool
	host        *Host
	registry    Registry
	runtime     Runtime
	cipher      *secret.Cipher
	now         func() time.Time
	cursorStore BrowseCursorStore
}

func NewSourceBrowser(pool *pgxpool.Pool, host *Host, cipher *secret.Cipher) (*SourceBrowser, error) {
	if pool == nil || host == nil || host.Registry == nil || host.Manager == nil || cipher == nil {
		return nil, errors.New("source browser requires retained host and data cipher")
	}
	return &SourceBrowser{pool: pool, host: host, registry: host.Registry, runtime: ManagedRuntime{host.Manager}, cipher: cipher, now: time.Now}, nil
}
func (b *SourceBrowser) retain(ctx context.Context, a auth.AdminContextClaims, expected storagesource.SourceConfig, generation uint64) error {
	tx, err := nativeDomainBegin(ctx, b.pool)
	if err != nil {
		return err
	}
	defer nativeDomainRollback(ctx, tx)
	_, err = resourcetenancy.NewStore(b.pool).RetainNativeResourcesTx(ctx, tx, &a, resourcetenancy.NativeResourceSet{Sources: []resourcetenancy.NativeSourceExpectation{{Source: expected, Generation: generation}}})
	if err != nil {
		return nativeDomainMap(err)
	}
	return nativeDomainMap(tx.Commit(ctx))
}
func (b *SourceBrowser) snapshot(ctx context.Context, a auth.AdminContextClaims, key uuid.UUID) (*plugins.NativeStorageSnapshot, error) {
	if b == nil || b.registry == nil || b.runtime == nil || b.cipher == nil || key == uuid.Nil {
		return nil, nativeDomainError("invalid_request")
	}
	tx, err := nativeDomainBegin(ctx, b.pool)
	if err != nil {
		return nil, err
	}
	defer nativeDomainRollback(ctx, tx)
	source, err := nativeDomainSourceTx(ctx, tx, key)
	if err != nil {
		return nil, nativeDomainMap(err)
	}
	if !source.Enabled || source.InstallationID == nil {
		return nil, nativeDomainError("not_found")
	}
	var generation uint64
	if err := tx.QueryRow(ctx, `SELECT runtime_generation FROM plugin_installations WHERE id=$1`, source.InstallationID).Scan(&generation); err != nil {
		return nil, nativeDomainMap(err)
	}
	_, err = resourcetenancy.NewStore(b.pool).RetainNativeResourcesTx(ctx, tx, &a, resourcetenancy.NativeResourceSet{Sources: []resourcetenancy.NativeSourceExpectation{{Source: source, Generation: generation}}})
	if err != nil {
		return nil, nativeDomainMap(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nativeDomainMap(err)
	}
	snapshot, err := b.registry.Snapshot(ctx, source.Key, source.OwnerID)
	if err != nil {
		return nil, nativeDomainMap(err)
	}
	if snapshot == nil || snapshot.Installation == nil || !SameSource(snapshot.Source, source) || snapshot.Generation != generation {
		return nil, nativeDomainError("not_found")
	}
	return snapshot, nil
}
func (b *SourceBrowser) recheck(ctx context.Context, a auth.AdminContextClaims, snapshot *plugins.NativeStorageSnapshot) error {
	current, err := b.registry.Snapshot(ctx, snapshot.Source.Key, snapshot.Source.OwnerID)
	if err != nil {
		return nativeDomainMap(err)
	}
	if !sameScanSnapshot(snapshot, current) {
		return nativeDomainError("invalid_selection_reference")
	}
	return b.retain(ctx, a, snapshot.Source, snapshot.Generation)
}
func (b *SourceBrowser) borrow(ctx context.Context, a auth.AdminContextClaims, snapshot *plugins.NativeStorageSnapshot) (Lease, context.Context, func(), error) {
	release, err := b.host.AcquireOpen()
	if err != nil {
		return Lease{}, nil, nil, nativeDomainError("native_storage_unavailable")
	}
	ioCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	lease, err := b.runtime.Borrow(ioCtx, storageplugin.Snapshot{InstallationID: snapshot.Installation.ID, Generation: snapshot.Generation, BinaryPath: snapshot.Installation.InstallPath, ExpectedChecksum: snapshot.ArtifactChecksum, Manifest: snapshot.Manifest, Config: snapshot.Config, Enabled: true, NativeOnly: true})
	if err == nil {
		err = ValidateReadDescription(lease.Description, snapshot.Source.ProviderSourceID, snapshot.Source.RootEntryID, snapshot)
	}
	if err == nil && (lease.Context == nil || lease.Provider == nil) {
		err = storageplugin.ErrUnavailable
	}
	if err == nil {
		err = b.recheck(ioCtx, a, snapshot)
	}
	if err != nil {
		cancel()
		release()
		return Lease{}, nil, nil, nativeDomainMap(err)
	}
	stop := context.AfterFunc(lease.Context, cancel)
	cleanup := func() { stop(); cancel(); release() }
	if lease.Context.Err() != nil {
		cleanup()
		return Lease{}, nil, nil, nativeDomainError("native_storage_unavailable")
	}
	return lease, ioCtx, cleanup, nil
}
func directoryOnlyRequired(description *storagev1.DescribeResponse, sourceID string) (bool, error) {
	for _, s := range description.GetSources() {
		if s.GetId() != sourceID {
			continue
		}
		audio := false
		for _, kind := range s.GetMediaKinds() {
			if kind == "audiobooks" {
				audio = true
			}
		}
		if audio && !s.GetDirectoryOnlyListing() {
			return false, nativeDomainError("native_storage_unavailable")
		}
		return s.GetDirectoryOnlyListing(), nil
	}
	return false, nativeDomainError("native_storage_unavailable")
}
func (b *SourceBrowser) statDirectory(ctx context.Context, a auth.AdminContextClaims, snapshot *plugins.NativeStorageSnapshot, lease Lease, id string) (*storagev1.Entry, error) {
	response, err := lease.Provider.Stat(ctx, &storagev1.StatRequest{SourceId: snapshot.Source.ProviderSourceID, EntryId: id}, grpc.MaxCallRecvMsgSize(1<<20))
	if check := b.recheck(ctx, a, snapshot); check != nil {
		return nil, check
	}
	if err != nil {
		return nil, nativeDomainError("native_storage_unavailable")
	}
	entry := response.GetEntry()
	if response == nil || proto.Size(response) > 1<<20 || entry == nil || entry.GetId() != id || entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_DIRECTORY || !nativeOpaqueID(entry.Id) || !safeBrowseText(entry.Name, 4096, true) || !safeBrowseText(entry.LogicalPath, 64<<10, false) || !safeBrowseText(entry.Revision, 4096, false) {
		return nil, nativeDomainError("invalid_selection_reference")
	}
	return entry, nil
}
func digestBrowsePage(page *storagev1.ListResponse) string {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(page)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func (b *SourceBrowser) Browse(ctx context.Context, a auth.AdminContextClaims, key uuid.UUID, request BrowseRequest) (BrowsePage, error) {
	out := BrowsePage{Breadcrumbs: make([]BrowseDirectory, 0), Directories: make([]BrowseDirectory, 0)}
	limit, err := nativePageLimit(request.Limit)
	if err != nil {
		return out, err
	}
	if len(request.DirectoryRef)+len(request.Cursor)+128 > MaxBrowseQueryBytes || (request.Cursor != "" && request.DirectoryRef == "") {
		return out, nativeDomainError("invalid_request")
	}
	snapshot, err := b.snapshot(ctx, a, key)
	if err != nil {
		return out, err
	}
	ref := b.newReference(a, snapshot.Source, snapshot.Generation)
	if request.DirectoryRef != "" {
		if err := b.openBrowse(request.DirectoryRef, selectionPurpose, &ref); err != nil {
			return out, err
		}
		if err := b.validateReference(ref, a, snapshot.Source, snapshot.Generation); err != nil {
			return out, err
		}
	}
	lease, ioCtx, cleanup, err := b.borrow(ctx, a, snapshot)
	if err != nil {
		return out, err
	}
	defer cleanup()
	directoryOnly, err := directoryOnlyRequired(lease.Description, snapshot.Source.ProviderSourceID)
	if err != nil {
		return out, err
	}
	// Every label comes from fresh directory facts. No names or logical paths
	// live in navigation tokens, and no logical path becomes an entry ID.
	for i, id := range ref.Ancestry {
		entry, err := b.statDirectory(ioCtx, a, snapshot, lease, id)
		if err != nil {
			return out, err
		}
		ancestor := ref
		ancestor.Ancestry = ref.Ancestry[:i+1]
		directory, err := b.directory(ancestor, entry.Name, entry.LogicalPath)
		if err != nil {
			return out, err
		}
		if i == len(ref.Ancestry)-1 {
			out.Current = directory
		} else {
			out.Breadcrumbs = append(out.Breadcrumbs, directory)
		}
	}
	state := browseCursorState{}
	previous := browseCursor{}
	if request.Cursor != "" {
		previous, state, err = b.loadCursor(ioCtx, a, ref, request.Cursor, limit)
		if check := b.recheck(ioCtx, a, snapshot); check != nil {
			return out, check
		}
		if err != nil {
			return out, err
		}
	}
	seenIDs := map[string]bool{}
	for _, id := range state.SeenIDs {
		seenIDs[id] = true
	}
	seenCursors := map[string]bool{}
	for _, cursor := range state.SeenCursors {
		seenCursors[cursor] = true
	}
	for calls := 0; len(out.Directories) < limit && !state.Complete && calls < 8; calls++ {
		cursorDigest := digestBrowse(state.ProviderCursor)
		if !state.Partial && (len(state.SeenCursors) >= maxBrowsePages || seenCursors[cursorDigest]) {
			return out, nativeDomainError("browse_limit_exceeded")
		}
		page, err := lease.Provider.List(ioCtx, &storagev1.ListRequest{SourceId: snapshot.Source.ProviderSourceID, DirectoryId: ref.Ancestry[len(ref.Ancestry)-1], Cursor: state.ProviderCursor, MaxEntries: uint32(limit), DirectoriesOnly: true}, grpc.MaxCallRecvMsgSize(1<<20))
		if check := b.recheck(ioCtx, a, snapshot); check != nil {
			return out, check
		}
		if err != nil {
			return out, nativeDomainError("native_storage_unavailable")
		}
		if err := mediasource.ValidatePage(state.ProviderCursor, page); err != nil {
			return out, nativeDomainError("invalid_provider_page")
		}
		if directoryOnly && (page.GetChangeToken() != "" || len(page.GetRemovedEntryIds()) != 0) {
			return out, nativeDomainError("invalid_provider_page")
		}
		digest := digestBrowsePage(page)
		if digest == "" {
			return out, nativeDomainError("invalid_provider_page")
		}
		if state.Partial {
			if state.PageDigest != digest || state.Offset >= len(page.GetEntries()) {
				return out, nativeDomainError("browse_restart_required")
			}
		} else {
			seenCursors[cursorDigest] = true
			state.SeenCursors = append(state.SeenCursors, cursorDigest)
			for _, entry := range page.GetEntries() {
				idDigest := digestBrowse(entry.Id)
				if seenIDs[idDigest] {
					return out, nativeDomainError("invalid_provider_page")
				}
				if len(state.SeenIDs) >= maxBrowseHistory {
					return out, nativeDomainError("browse_limit_exceeded")
				}
				seenIDs[idDigest] = true
				state.SeenIDs = append(state.SeenIDs, idDigest)
				if directoryOnly && entry.Kind != storagev1.EntryKind_ENTRY_KIND_DIRECTORY {
					return out, nativeDomainError("invalid_provider_page")
				}
				if entry.Kind == storagev1.EntryKind_ENTRY_KIND_DIRECTORY {
					if !safeBrowseText(entry.Name, 4096, true) || !safeBrowseText(entry.LogicalPath, 64<<10, false) {
						return out, nativeDomainError("invalid_provider_page")
					}
					for _, ancestor := range ref.Ancestry {
						if entry.Id == ancestor {
							return out, nativeDomainError("invalid_provider_page")
						}
					}
				}
			}
		}
		if !page.GetComplete() && seenCursors[digestBrowse(page.GetNextCursor())] {
			return out, nativeDomainError("invalid_provider_page")
		}
		nextOffset := state.Offset
		for ; nextOffset < len(page.GetEntries()) && len(out.Directories) < limit; nextOffset++ {
			entry := page.GetEntries()[nextOffset]
			if entry.Kind != storagev1.EntryKind_ENTRY_KIND_DIRECTORY {
				continue
			}
			if len(ref.Ancestry) >= maxBrowseDepth {
				return out, nativeDomainError("browse_limit_exceeded")
			}
			child := ref
			child.Ancestry = append(append([]string(nil), ref.Ancestry...), entry.Id)
			directory, err := b.directory(child, entry.Name, entry.LogicalPath)
			if err != nil {
				return out, err
			}
			out.Directories = append(out.Directories, directory)
		}
		if nextOffset < len(page.GetEntries()) {
			state.Partial = true
			state.Offset = nextOffset
			state.PageDigest = digest
			break
		}
		state.Partial = false
		state.Offset = 0
		state.PageDigest = ""
		state.ProviderCursor = page.GetNextCursor()
		state.Complete = page.GetComplete()
	}
	if !state.Complete {
		out.NextCursor, err = b.saveCursor(ioCtx, a, ref, state, limit, previous)
		if check := b.recheck(ioCtx, a, snapshot); check != nil {
			return out, check
		}
		if err != nil {
			return out, err
		}
	} else if previous.StateID != uuid.Nil {
		err = b.cursorStore.Consume(ioCtx, browseStateKey(previous.StateID), browseActorKey(a))
		if check := b.recheck(ioCtx, a, snapshot); check != nil {
			return out, check
		}
		if err != nil {
			return out, nativeDomainError("browse_restart_required")
		}
	}
	if err := ioCtx.Err(); err != nil {
		return BrowsePage{}, nativeDomainError("native_storage_unavailable")
	}
	if ref.ExpiresAt <= b.now().Unix() {
		return BrowsePage{}, nativeDomainError("invalid_selection_reference")
	}
	return out, nil
}
