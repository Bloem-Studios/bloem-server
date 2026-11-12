package handlers

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"sync"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/subtitles"
)

// NativeSidecarStore lists the subtitle sidecars the series scan attached to
// a native file (storagesource.Repository.FileSidecars). It grants nothing.
type NativeSidecarStore interface {
	FileSidecars(context.Context, int) ([]storagesource.Sidecar, error)
}

// nativeSidecarPathPrefix marks the opaque path a native sidecar is listed
// under. It names no local file; only nativeSidecarFor maps it back.
const nativeSidecarPathPrefix = "bloem-storage-sidecar:"

// nativeSidecarPath is the stable listed path of a sidecar: its location and
// entry, never its revision, so a playback pinned to it reads the sidecar's
// current revision; its base name is the sidecar's file name.
func nativeSidecarPath(s storagesource.Sidecar) string {
	digest := sha256.Sum256(append(s.LocationID[:], s.EntryID...))
	name := path.Base("/" + s.Name)
	if name == "/" {
		name = "subtitle." + s.Format
	}
	return nativeSidecarPathPrefix + hex.EncodeToString(digest[:]) + "/" + name
}

// nativeExternalSubtitles lists a native file's sidecars where Silo lists a
// local file's external subtitles, in the same shape.
func nativeExternalSubtitles(sidecars []storagesource.Sidecar) []models.ExternalSubtitle {
	out := make([]models.ExternalSubtitle, 0, len(sidecars))
	for _, s := range sidecars {
		listed := nativeSidecarPath(s)
		out = append(out, models.ExternalSubtitle{Path: listed, Language: s.Language, Format: s.Format, Title: path.Base(listed), Forced: s.Forced, HearingImpaired: s.HearingImpaired})
	}
	return out
}

// withNativeSidecars replaces a native file's catalog external subtitles,
// which never name a readable file, with the sidecars the scan recorded.
func withNativeSidecars(ctx context.Context, store NativeSidecarStore, file *models.MediaFile) (*models.MediaFile, error) {
	if store == nil || file == nil || !storagesource.IsNativeLocation(file.FilePath) {
		return file, nil
	}
	sidecars, err := store.FileSidecars(ctx, file.ID)
	if err != nil {
		return nil, err
	}
	listed := *file
	listed.ExternalSubtitles = nativeExternalSubtitles(sidecars)
	return &listed, nil
}

// catalog is the resolver playback plans and subtitle routes read native
// files through: supported files only, with their sidecars listed.
func (s *NativeVideoService) catalog() FilePathResolver {
	return nativeVideoCatalogFiles{FilePathResolver: s.Files, sidecars: s.Sidecars}
}

// nativeSidecarFor finds the current sidecar row behind a listed path.
func (s *NativeVideoService) nativeSidecarFor(ctx context.Context, fileID int, listed string) (storagesource.Sidecar, error) {
	if s.Sidecars == nil {
		return storagesource.Sidecar{}, catalog.ErrItemNotFound
	}
	sidecars, err := s.Sidecars.FileSidecars(ctx, fileID)
	if err != nil {
		return storagesource.Sidecar{}, err
	}
	for _, sidecar := range sidecars {
		if sidecar.MediaFileID == fileID && nativeSidecarPath(sidecar) == listed {
			return sidecar, nil
		}
	}
	return storagesource.Sidecar{}, catalog.ErrItemNotFound
}

// sameNativeSidecarSource reports whether opened read the sidecar from the
// bound file's source, runtime and catalog identity, at the sidecar's entry.
func sameNativeSidecarSource(opened, bound nativestorage.PlaybackIdentity) bool {
	opened.Reference, bound.Reference = storagesource.PersistedRef{}, storagesource.PersistedRef{}
	return opened == bound
}

// readNativeSidecar returns a sidecar's bytes, read through the storage
// plugin with the session's bound authority. Bytes are cached by the bound
// source and the sidecar's entry and revision, never by path, and a cache
// hit still requires a live binding; callers recheck the session's source
// after any read.
func (s *NativeVideoService) readNativeSidecar(ctx context.Context, session *playback.Session, file *models.MediaFile, sidecar storagesource.Sidecar) ([]byte, error) {
	if s == nil || s.Bindings == nil || s.OpenSidecar == nil || session == nil || file == nil || sidecar.MediaFileID != file.ID {
		return nil, nativeVideoUnavailable()
	}
	if s.Deny.Denied(ctx, session.ID) {
		return nil, errPlaybackSessionEnded
	}
	binding, err := s.Bindings.Get(ctx, session.ID, file.ID)
	if err != nil {
		return nil, err
	}
	if binding.SessionID != session.ID || binding.UserID != session.UserID || binding.ProfileID != session.ProfileID || binding.Identity.FileID != file.ID || binding.Identity.FilePath != file.FilePath {
		return nil, catalog.ErrItemNotFound
	}
	source := binding.Identity
	source.Reference = storagesource.PersistedRef{}
	identity, err := json.Marshal(struct {
		Source  nativestorage.PlaybackIdentity
		Sidecar storagesource.PersistedRef
	}{source, sidecar.PersistedRef})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(identity)
	key := hex.EncodeToString(digest[:])
	if data, ok := nativeSidecarBytes.get(key); ok {
		return data, nil
	}
	opened, err := s.OpenSidecar(ctx, file, binding, sidecar.PersistedRef)
	if err != nil {
		return nil, err
	}
	defer func() { _ = opened.Close() }()
	info := opened.Info()
	if !sameNativeSidecarSource(opened.Identity(), binding.Identity) || opened.Identity().Reference != sidecar.PersistedRef || info.Revision != sidecar.Revision {
		return nil, storagesource.ErrReferenceConflict
	}
	if info.Size < 0 || info.Size > nativeSidecarMaxBytes {
		return nil, errNativeSidecarTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(opened, nativeSidecarMaxBytes+1))
	if err == nil && int64(len(data)) != info.Size {
		err = storagesource.ErrReferenceConflict
	}
	if err == nil {
		err = opened.Reauthorize(ctx)
	}
	if err == nil {
		err = opened.Err()
	}
	if err != nil {
		return nil, err
	}
	nativeSidecarBytes.put(key, data)
	return data, nil
}

// serveNativeSidecar answers a native file's external subtitle, as Silo
// answers a local sidecar: SRT as WebVTT or, on request, as itself, ASS/SSA
// raw or as WebVTT, after the timing correction stored for its bytes. Only a
// session with a live native binding reaches the provider.
func (h *StreamHandler) serveNativeSidecar(w http.ResponseWriter, r *http.Request, session *playback.Session, file *models.MediaFile, index int, requestedFormat string) {
	sub := file.ExternalSubtitles[index]
	if !subtitleSidecarFormatSupported(sub.Format, requestedFormat, false) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
			"Requested subtitle extension does not match the selected track")
		return
	}
	if h.NativeVideo == nil {
		writeAPIError(w, nativeVideoUnavailable())
		return
	}
	sidecar, err := h.NativeVideo.nativeSidecarFor(r.Context(), file.ID, sub.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Subtitle track not found")
		return
	}
	if r.Method == http.MethodHead {
		writeSubtitleRepresentationHead(w, subtitleRepresentationFormat(requestedFormat, servesOriginalSubRip(r, sub.Format, requestedFormat)))
		return
	}
	ctx, finish := h.nativeSubtitleContext(r.Context(), session)
	defer finish()
	data, err := h.NativeVideo.readNativeSidecar(ctx, session, file, sidecar)
	if err == nil {
		err = h.subtitleFilePreflight(ctx, session, file)
	}
	if err != nil {
		if errors.Is(err, errNativeSidecarTooLarge) {
			writeError(w, http.StatusUnprocessableEntity, "subtitle_too_large", "Subtitle file is too large")
			return
		}
		writeAPIError(w, nativeVideoDeliveryError(err))
		return
	}
	data, err = subtitles.ExternalDeliveryBytes(ctx, h.ExternalTimings, file.ID, subtitles.SubtitleFormat(sub.Format), sub.Language, data)
	if err != nil {
		slog.ErrorContext(ctx, "load native sidecar subtitle failed", "component", "api", "file_id", file.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load external subtitle")
		return
	}
	// A new revision of the sidecar is served behind the same URL.
	w.Header().Set("Cache-Control", "private, no-cache")
	h.serveSubtitleData(w, r, sub.Format, data, requestedFormat)
}

var errNativeSidecarTooLarge = errors.New("native subtitle sidecar is too large")

const (
	// nativeSidecarMaxBytes bounds one sidecar read; text subtitles are far
	// smaller.
	nativeSidecarMaxBytes = 8 << 20
	// nativeSidecarCacheBytes bounds the sidecar bytes a server keeps.
	nativeSidecarCacheBytes = 32 << 20
)

// nativeSidecarBytes is the server's sidecar cache. Its keys name the bound
// source and the sidecar's revision, so it is shared by every service.
var nativeSidecarBytes nativeSidecarCache

// nativeSidecarCache keeps recently read sidecar bytes, least recently used
// first out. It holds bytes only, never access.
type nativeSidecarCache struct {
	mu    sync.Mutex
	order list.List
	items map[string]*list.Element
	bytes int
}

type nativeSidecarCacheEntry struct {
	key  string
	data []byte
}

func (c *nativeSidecarCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(element)
	return bytes.Clone(element.Value.(*nativeSidecarCacheEntry).data), true
}

func (c *nativeSidecarCache) put(key string, data []byte) {
	if len(data) > nativeSidecarMaxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[string]*list.Element)
	}
	if element, ok := c.items[key]; ok {
		c.order.MoveToFront(element)
		return
	}
	c.items[key] = c.order.PushFront(&nativeSidecarCacheEntry{key: key, data: bytes.Clone(data)})
	c.bytes += len(data)
	for c.bytes > nativeSidecarCacheBytes {
		oldest := c.order.Back()
		entry := oldest.Value.(*nativeSidecarCacheEntry)
		c.order.Remove(oldest)
		delete(c.items, entry.key)
		c.bytes -= len(entry.data)
	}
}
