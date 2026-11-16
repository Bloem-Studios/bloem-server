package handlers

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/httpstream"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/nodecontrol"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/nodesource"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/remotesource"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// Native inputs (storage-source files and `.strm` items) run on Silo's
// transcode nodes through Silo's core source access: the node runs only
// Silo's code and holds no storage credentials. The API servers mint a Bloem
// source grant for a native file (NativeNodeSources.Granter), answer the
// node's sources/open and source bytes requests for it by opening the file
// through the native video host under the playback's (or download's) sealed
// authority, and leave every other grant to Silo's handlers. Only a node with
// source_access=core can take a native input. See
// docs/architecture/bloem-native-video-playback.md#where-native-inputs-run.

// nativeNodeGrantPrefix starts every Bloem source grant. Silo's grants are
// base64url payload "." MAC and never contain "-" before the first ".", so
// the two formats cannot be confused.
const nativeNodeGrantPrefix = "bloem-native-grant.v1."

// nativeNodeGrantKeyLabel is the HKDF info string of the grant sealing key,
// separate from Silo's grant MAC key derived from the same secret.
const nativeNodeGrantKeyLabel = "bloem/native-node-source-grant/v1"

// maxNativeNodeGrantBytes bounds an encoded grant.
const maxNativeNodeGrantBytes = 4 << 10

// maxNativeNodeStreams caps the native source byte responses one API server
// serves one node at once, as Silo caps its own (nodecontrol's
// maxSourceStreamsPerNode); each holds an open native file and its read-ahead.
const maxNativeNodeStreams = 128

var (
	// errNativeNodeLocalAccess is a native input refused for a node that reads
	// sources through its own library mount: a native input has no file there.
	errNativeNodeLocalAccess = errors.New("a native input needs a transcode node with core source access")
	// errNativeNodeNoSession is a native grant asked for outside a playback or
	// download preparation the API server can name.
	errNativeNodeNoSession = errors.New("a native source grant needs the playback or preparation it reads for")
	// errNativeNodeUnavailable is a native grant asked for before the native
	// video host is installed.
	errNativeNodeUnavailable = errors.New("native node sources are not composed")
)

// nativeNodeGrant is a Bloem source grant's sealed payload. The node cannot
// read it: it names the catalog reference and the binding of a playback.
type nativeNodeGrant struct {
	Node     int    `json:"n"`
	File     int    `json:"f"`
	Session  string `json:"s"`
	Path     string `json:"p"`
	Revision string `json:"r"`
	Size     int64  `json:"z"`
	MTimeNS  int64  `json:"m"`
	Iat      int64  `json:"i"`
	Exp      int64  `json:"e"`
}

// expires is when the grant stops being valid: its expiry, or the end of its
// whole life (nodesource.MaxGrantAge), whichever comes first.
func (g nativeNodeGrant) expires() time.Time {
	return time.Unix(min(g.Exp, g.Iat+int64(nodesource.MaxGrantAge/time.Second)), 0)
}

// etag is the protocol entity tag of the granted revision. The revision is
// hashed: a storage version id is not something the node needs to see.
func (g nativeNodeGrant) etag() string {
	return fmt.Sprintf(`"bloem-%d-%d-%s"`, g.File, g.Size, g.revisionDigest())
}

// path is the opaque absolute path the node keys its relay on.
func (g nativeNodeGrant) path() string {
	return fmt.Sprintf("/bloem-native/%d/%s", g.File, g.revisionDigest())
}

func (g nativeNodeGrant) revisionDigest() string {
	sum := sha256.Sum256([]byte(g.Path + "\x00" + g.Revision))
	return hex.EncodeToString(sum[:12])
}

func (g nativeNodeGrant) wellFormed() bool {
	return g.Node > 0 && g.File > 0 && g.Session != "" && storagesource.IsNativeLocation(g.Path) && g.Revision != "" && g.Size >= 0 && g.Iat > 0 && g.Exp > 0
}

// IsNativeNodeGrant reports whether token is a Bloem source grant.
func IsNativeNodeGrant(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), nativeNodeGrantPrefix)
}

// NativeNodeSourceConfig is what NativeNodeSources needs once the native
// video host is composed.
type NativeNodeSourceConfig struct {
	Service *NativeVideoService
	// Secret is the server secret the API servers share; the grant sealing
	// key is derived from it.
	Secret func() string
	// Node loads a transcode node row, for its source access.
	Node func(ctx context.Context, id int) (*nodepool.Node, error)
	// Recipes loads the recipe card of a transport, for a node's resolve.
	Recipes func(ctx context.Context, transportID string) (*playback.RecipeCard, bool)
	// Lifetime ends with the API server; source byte responses are cut then.
	Lifetime context.Context //nolint:containedctx // the server's lifetime, as nodecontrol.Options.Lifetime
}

// NativeNodeSources mints and serves Bloem source grants. The zero value is
// usable: until Install runs it mints nothing for native files and refuses
// their grants with 503.
type NativeNodeSources struct {
	cfg     atomic.Pointer[NativeNodeSourceConfig]
	now     func() time.Time
	streams nativeNodeStreams
}

// Install composes the native video host. It may run after the granters and
// routes were wired.
func (s *NativeNodeSources) Install(cfg NativeNodeSourceConfig) {
	if s == nil || cfg.Service == nil || cfg.Secret == nil || cfg.Node == nil {
		return
	}
	s.cfg.Store(&cfg)
}

func (s *NativeNodeSources) config() *NativeNodeSourceConfig {
	if s == nil {
		return nil
	}
	return s.cfg.Load()
}

func (s *NativeNodeSources) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Ready reports whether native inputs can be granted to transcode nodes.
func (s *NativeNodeSources) Ready() bool { return s.config() != nil }

// Granter returns silo wrapped so a native file gets a Bloem grant and every
// other file Silo's grant, unchanged. A nil silo or a nil s returns silo.
func (s *NativeNodeSources) Granter(silo nodesource.FileGranter) nodesource.FileGranter {
	if s == nil || silo == nil {
		return silo
	}
	if grants, ok := silo.(*nodesource.Grants); ok && grants == nil {
		return silo
	}
	if wrapped, ok := silo.(nativeNodeGranter); ok {
		return wrapped
	}
	return nativeNodeGranter{sources: s, silo: silo}
}

// GrantIssuer wraps nodecontrol's grant issuer the same way, for the grant a
// node's resolve mints.
func (s *NativeNodeSources) GrantIssuer(silo nodecontrol.GrantIssuer) nodecontrol.GrantIssuer {
	if s == nil || silo == nil {
		return silo
	}
	if grants, ok := silo.(*nodesource.Grants); ok && grants == nil {
		return silo
	}
	return nativeNodeGrantIssuer{GrantIssuer: silo, sources: s}
}

// FileCatalog wraps nodecontrol's catalog check so a Silo grant can never
// open a native file: Silo would serve a `.strm` file's text, which names its
// address. A node's resolve still finds a native recipe's row active, and
// then gets a Bloem grant from GrantIssuer.
func (s *NativeNodeSources) FileCatalog(silo nodecontrol.FileCatalog) nodecontrol.FileCatalog {
	if s == nil || silo == nil {
		return silo
	}
	return nativeNodeFileCatalog{FileCatalog: silo}
}

type nativeNodeFileCatalog struct{ nodecontrol.FileCatalog }

func (c nativeNodeFileCatalog) IsActiveFile(ctx context.Context, id int, path string) (bool, error) {
	if storagesource.IsNativeLocation(path) {
		if _, resolving := ctx.Value(nativeNodeResolveKey{}).(string); !resolving {
			return false, nil
		}
	}
	return c.FileCatalog.IsActiveFile(ctx, id, path)
}

type nativeNodeGranter struct {
	sources *NativeNodeSources
	silo    nodesource.FileGranter
}

func (g nativeNodeGranter) ForFile(ctx context.Context, nodeID int, file *models.MediaFile) (string, error) {
	if file == nil || !storagesource.IsNativeLocation(file.FilePath) {
		return g.silo.ForFile(ctx, nodeID, file)
	}
	return g.sources.forNativeFile(ctx, nodeID, file)
}

type nativeNodeGrantIssuer struct {
	nodecontrol.GrantIssuer
	sources *NativeNodeSources
}

func (g nativeNodeGrantIssuer) ForFile(ctx context.Context, nodeID int, file *models.MediaFile) (string, error) {
	if file == nil || !storagesource.IsNativeLocation(file.FilePath) {
		return g.GrantIssuer.ForFile(ctx, nodeID, file)
	}
	grant, err := g.sources.forNativeFile(ctx, nodeID, file)
	if err != nil && ctx.Err() == nil {
		// nodecontrol answers a resolve whose source cannot be granted as a
		// changed source, which ends the node's rebuild; never as a Silo grant.
		return "", fmt.Errorf("%w: %w", nodesource.ErrSourceGone, err)
	}
	return grant, err
}

type nativeNodeSessionKey struct{}
type nativeNodeResolveKey struct{}

// WithNativeNodeGrantSession names the playback session (or download
// preparation binding) whose native binding a grant minted under ctx reads
// through.
func WithNativeNodeGrantSession(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, nativeNodeSessionKey{}, sessionID)
}

// nativeNodeGrantRequest is r carrying session for the grant its remote
// start mints.
func nativeNodeGrantRequest(r *http.Request, session *playback.Session) *http.Request {
	if r == nil || session == nil || session.ID == "" {
		return r
	}
	return r.WithContext(WithNativeNodeGrantSession(r.Context(), session.ID))
}

// grantSession names the binding a grant minted under ctx for file reads
// through: one named by WithNativeNodeGrantSession, the playback session of a
// node's resolve, or the playback session of the stream token the current
// request carries (a move off an unreachable node).
func (s *NativeNodeSources) grantSession(ctx context.Context, cfg *NativeNodeSourceConfig, file *models.MediaFile) (string, error) {
	if session, ok := ctx.Value(nativeNodeSessionKey{}).(string); ok && session != "" {
		return session, nil
	}
	if transport, ok := ctx.Value(nativeNodeResolveKey{}).(string); ok && transport != "" {
		if cfg.Recipes == nil {
			return "", errNativeNodeNoSession
		}
		card, found := cfg.Recipes(ctx, transport)
		if !found || card == nil || card.SessionID == "" || card.MediaFileID != file.ID || card.InputPath != file.FilePath {
			return "", errNativeNodeNoSession
		}
		return card.SessionID, nil
	}
	if cfg.Service.Delegation != nil {
		if authority, err := cfg.Service.Delegation(ctx); err == nil && authority.PlaybackSessionID != "" {
			return authority.PlaybackSessionID, nil
		}
	}
	return "", errNativeNodeNoSession
}

func (s *NativeNodeSources) forNativeFile(ctx context.Context, nodeID int, file *models.MediaFile) (string, error) {
	cfg := s.config()
	if cfg == nil || cfg.Service.Bindings == nil {
		return "", errNativeNodeUnavailable
	}
	if err := s.requireCoreAccess(ctx, cfg, nodeID); err != nil {
		return "", err
	}
	session, err := s.grantSession(ctx, cfg, file)
	if err != nil {
		return "", err
	}
	binding, err := cfg.Service.Bindings.Get(ctx, session, file.ID)
	if err != nil {
		return "", err
	}
	if binding.SessionID != session || binding.Identity.FileID != file.ID || binding.Identity.FilePath != file.FilePath || binding.Info.Revision == "" || binding.Info.Size < 0 {
		return "", storagesource.ErrReferenceConflict
	}
	claims := nativeNodeGrant{Node: nodeID, File: file.ID, Session: session, Path: file.FilePath, Revision: binding.Info.Revision,
		Size: binding.Info.Size, MTimeNS: binding.Info.ModifiedAt.UnixNano(), Iat: s.clock().Unix()}
	return s.seal(cfg, claims)
}

// requireCoreAccess refuses a node that reads its sources from its own mount.
func (s *NativeNodeSources) requireCoreAccess(ctx context.Context, cfg *NativeNodeSourceConfig, nodeID int) error {
	node, err := cfg.Node(ctx, nodeID)
	if err != nil {
		return err
	}
	if node == nil || node.ID != nodeID || node.SourceAccess != nodepool.SourceAccessCore {
		return errNativeNodeLocalAccess
	}
	return nil
}

func (s *NativeNodeSources) key(cfg *NativeNodeSourceConfig) (cipher.AEAD, error) {
	secret := cfg.Secret()
	if strings.TrimSpace(secret) == "" {
		return nil, nodesource.ErrNoGrantKey
	}
	key, err := hkdf.Key(sha256.New, []byte(secret), nil, nativeNodeGrantKeyLabel, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *NativeNodeSources) seal(cfg *NativeNodeSourceConfig, claims nativeNodeGrant) (string, error) {
	aead, err := s.key(cfg)
	if err != nil {
		return "", err
	}
	claims.Exp = min(s.clock().Add(nodesource.GrantLifetime).Unix(), claims.Iat+int64(nodesource.MaxGrantAge/time.Second))
	if !claims.wellFormed() {
		return "", nodesource.ErrInvalidGrant
	}
	payload, _ := json.Marshal(claims) // strings and integers only
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, payload, []byte(nativeNodeGrantPrefix))
	token := nativeNodeGrantPrefix + base64.RawURLEncoding.EncodeToString(sealed)
	if len(token) > maxNativeNodeGrantBytes {
		return "", nodesource.ErrInvalidGrant
	}
	return token, nil
}

// open verifies token for node nodeID and returns its claims, or the status
// to refuse with: 403 for a grant that does not verify, has expired or is
// another node's, 503 without a key.
func (s *NativeNodeSources) open(cfg *NativeNodeSourceConfig, nodeID int, token string) (nativeNodeGrant, int) {
	aead, err := s.key(cfg)
	if err != nil {
		return nativeNodeGrant{}, http.StatusServiceUnavailable
	}
	token = strings.TrimSpace(token)
	encoded, ok := strings.CutPrefix(token, nativeNodeGrantPrefix)
	if !ok || len(token) > maxNativeNodeGrantBytes {
		return nativeNodeGrant{}, http.StatusForbidden
	}
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < aead.NonceSize() {
		return nativeNodeGrant{}, http.StatusForbidden
	}
	payload, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(nativeNodeGrantPrefix))
	if err != nil {
		return nativeNodeGrant{}, http.StatusForbidden
	}
	var claims nativeNodeGrant
	if err := json.Unmarshal(payload, &claims); err != nil || !claims.wellFormed() {
		return nativeNodeGrant{}, http.StatusForbidden
	}
	if !s.clock().Before(claims.expires()) || claims.Node != nodeID {
		return nativeNodeGrant{}, http.StatusForbidden
	}
	return claims, 0
}

// openGranted opens the granted native file under the binding's sealed
// authority, rechecked by the native video host as for any delegated read,
// and checks the catalog row and the revision. It returns the open file or
// the status to refuse with.
func (s *NativeNodeSources) openGranted(ctx context.Context, cfg *NativeNodeSourceConfig, claims nativeNodeGrant) (NativeVideoFile, int) {
	service := cfg.Service
	if service.Files == nil || service.Bindings == nil || service.OpenDelegated == nil {
		return nil, http.StatusServiceUnavailable
	}
	file, err := service.Files.GetByID(ctx, claims.File)
	switch {
	case errors.Is(err, catalog.ErrItemNotFound) || err == nil && file == nil:
		return nil, http.StatusGone
	case err != nil:
		return nil, http.StatusServiceUnavailable
	}
	if file.ID != claims.File || file.MissingSince != nil || file.FilePath != claims.Path || !NativeVideoSupported(file) {
		return nil, http.StatusGone
	}
	binding, err := service.Bindings.Get(ctx, claims.Session, claims.File)
	if err != nil {
		// A stopped playback or a finished preparation revokes its binding.
		if errors.Is(err, storagesource.ErrReferenceConflict) {
			return nil, http.StatusForbidden
		}
		return nil, http.StatusServiceUnavailable
	}
	if binding.SessionID != claims.Session || binding.Info.Revision != claims.Revision || binding.Info.Size != claims.Size {
		return nil, http.StatusPreconditionFailed
	}
	opened, err := service.OpenDelegated(ctx, file, binding)
	if err != nil {
		if opened != nil {
			_ = opened.Close()
		}
		return nil, nativeNodeSourceStatus(ctx, err)
	}
	if opened == nil {
		return nil, http.StatusServiceUnavailable
	}
	if err := opened.Reauthorize(ctx); err != nil {
		_ = opened.Close()
		return nil, nativeNodeSourceStatus(ctx, err)
	}
	if info := opened.Info(); info.Revision != claims.Revision || info.Size != claims.Size {
		_ = opened.Close()
		return nil, http.StatusPreconditionFailed
	}
	return opened, 0
}

// nativeNodeSourceStatus maps an open or authority failure to the status a
// node reads: 403 (refused, fatal), 410 (gone, fatal), 412 (changed, fatal)
// or 503 (retried). An address the `.strm` guard refuses is 403.
func nativeNodeSourceStatus(ctx context.Context, err error) int {
	var api *APIError
	switch {
	case ctx.Err() != nil:
		return http.StatusServiceUnavailable
	case errors.Is(err, remotesource.ErrBlockedAddress), errors.Is(err, remotesource.ErrInvalidStrm), errors.Is(err, errPlaybackSessionEnded):
		return http.StatusForbidden
	case errors.Is(err, storagesource.ErrReferenceConflict), errors.Is(err, remotesource.ErrChanged):
		return http.StatusPreconditionFailed
	case errors.Is(err, catalog.ErrItemNotFound):
		return http.StatusGone
	case errors.As(err, &api) && (api.Status == http.StatusForbidden || api.Status == http.StatusUnauthorized):
		return http.StatusForbidden
	case errors.As(err, &api) && api.Status == http.StatusNotFound:
		return http.StatusGone
	}
	var status interface{ HTTPStatus() int }
	if errors.As(err, &status) && (status.HTTPStatus() == http.StatusForbidden || status.HTTPStatus() == http.StatusUnauthorized) {
		return http.StatusForbidden
	}
	if isNativeCredentialRefusal(err) {
		return http.StatusForbidden
	}
	return http.StatusServiceUnavailable
}

// nativeCredentialRefusals are the authority refusals of the native video
// policy (internal/api/middleware's ErrCurrentCredential*), registered by the
// router; handlers cannot import that package.
var nativeCredentialRefusals atomic.Pointer[[]error]

// RegisterNativeCredentialRefusals names the errors a delegated native read
// answers a withdrawn authority with.
func RegisterNativeCredentialRefusals(errs ...error) {
	list := append([]error(nil), errs...)
	nativeCredentialRefusals.Store(&list)
}

func isNativeCredentialRefusal(err error) bool {
	list := nativeCredentialRefusals.Load()
	if list == nil {
		return false
	}
	for _, target := range *list {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// logRefusal records a refused native source request. It never logs the
// error: a native open error may name a storage location or an address.
func logRefusal(ctx context.Context, route string, nodeID int, claims nativeNodeGrant, status int) {
	level := slog.LevelDebug
	if status == http.StatusServiceUnavailable {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, "native node source refused", "component", "nativevideo", "route", route,
		"node_id", nodeID, "media_file_id", claims.File, "status", status)
}

// pathSourceOpen is Silo's sources/open route.
const pathSourceOpen = nodecontrol.PathPrefix + "self/sources/open"

// pathResolve is Silo's resolve route.
const pathResolve = nodecontrol.PathPrefix + "self/resolve"

// NodeSelfMiddleware sits inside Silo's /api/internal/nodes/self routes,
// after Silo's node authentication. A sources/open request with a Bloem grant
// is answered here (through limit, Silo's per-node playback budget); a
// resolve is marked so a native recipe's grant names its playback; anything
// else goes to Silo's handler untouched.
func (s *NativeNodeSources) NodeSelfMiddleware(limit func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if s == nil {
			return next
		}
		var open http.Handler = http.HandlerFunc(s.serveOpen)
		if limit != nil {
			open = limit(open)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != pathSourceOpen && r.URL.Path != pathResolve {
				next.ServeHTTP(w, r)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, nodecontrol.MaxBodyBytes+1))
			_ = r.Body.Close()
			if err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			if r.URL.Path == pathResolve {
				var req nodecontrol.ResolveRequest
				if json.Unmarshal(body, &req) == nil && req.TransportID != "" {
					r = r.WithContext(context.WithValue(r.Context(), nativeNodeResolveKey{}, req.TransportID))
				}
				next.ServeHTTP(w, r)
				return
			}
			var req nodesource.OpenRequest
			if json.Unmarshal(body, &req) != nil || !IsNativeNodeGrant(req.Grant) {
				next.ServeHTTP(w, r)
				return
			}
			open.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nativeNodeOpenKey{}, req.Grant)))
		})
	}
}

type nativeNodeOpenKey struct{}

// serveOpen answers sources/open for a Bloem grant with Silo's
// nodesource.OpenResponse: a renewed grant for the same revision, an opaque
// path and the revision. It rechecks the node's source access, the catalog,
// the binding and the authority on every call, so at every renewal.
func (s *NativeNodeSources) serveOpen(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	nodeID, ok := nodecontrol.NodeID(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	cfg := s.config()
	if cfg == nil {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	token, _ := r.Context().Value(nativeNodeOpenKey{}).(string)
	claims, status := s.open(cfg, nodeID, token)
	if status == 0 {
		if err := s.requireCoreAccess(r.Context(), cfg, nodeID); err != nil {
			status = http.StatusForbidden
			if !errors.Is(err, errNativeNodeLocalAccess) && !errors.Is(err, nodepool.ErrNodeNotFound) {
				status = http.StatusServiceUnavailable
			}
		}
	}
	if status == 0 {
		var opened NativeVideoFile
		opened, status = s.openGranted(r.Context(), cfg, claims)
		if opened != nil {
			_ = opened.Close()
		}
	}
	if status != 0 {
		logRefusal(r.Context(), "open", nodeID, claims, status)
		http.Error(w, http.StatusText(status), status)
		return
	}
	renewed, err := s.seal(cfg, claims)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(nodesource.OpenResponse{Grant: renewed, Path: claims.path(), Size: claims.Size, ModTimeUnixNano: claims.MTimeNS, ETag: claims.etag()}); err != nil {
		slog.DebugContext(r.Context(), "write native source open answer", "component", "nativevideo", "node_id", nodeID)
	}
}

// SourceBytesMiddleware sits in front of every route and takes Silo's source
// bytes route for a Bloem grant: it authenticates the node with Silo's
// nodecontrol.RequireNode and serves the bytes. Every other request, and the
// route with a Silo grant, goes on untouched.
func (s *NativeNodeSources) SourceBytesMiddleware(keys nodecontrol.KeySource) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if s == nil || keys == nil {
			return next
		}
		serve := nodecontrol.RequireNode(keys)(http.HandlerFunc(s.serveBytes))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == nodesource.PathSources && IsNativeNodeGrant(r.Header.Get(nodesource.HeaderSourceGrant)) {
				serve.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// nativeNodeIgnoredConditions are dropped as Silo's source route drops them:
// If-Match on the grant's entity tag is the only condition a node may set.
var nativeNodeIgnoredConditions = []string{"If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "If-Range"}

// serveBytes serves a Bloem grant's bytes with Silo's source route semantics:
// single and suffix ranges, If-Match on the grant's entity tag, 416 with the
// size and ETag, 412 for another revision, no-store on every answer, 429
// over the per-node cap, and a write deadline that rolls with progress. The
// bytes come through the native video host's read-ahead, whose authority is
// rechecked at least once a second while it reads.
func (s *NativeNodeSources) serveBytes(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := nodecontrol.NodeID(r.Context())
	if !ok {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	cfg := s.config()
	if cfg == nil || cfg.Lifetime != nil && cfg.Lifetime.Err() != nil {
		refuseNativeNodeSource(w, http.StatusServiceUnavailable)
		return
	}
	if !s.streams.acquire(nodeID, maxNativeNodeStreams) {
		w.Header().Set("Retry-After", "1")
		refuseNativeNodeSource(w, http.StatusTooManyRequests)
		return
	}
	defer s.streams.release(nodeID)
	claims, status := s.open(cfg, nodeID, r.Header.Get(nodesource.HeaderSourceGrant))
	var opened NativeVideoFile
	if status == 0 {
		opened, status = s.openGranted(r.Context(), cfg, claims)
	}
	if status != 0 {
		logRefusal(r.Context(), "bytes", nodeID, claims, status)
		refuseNativeNodeSource(w, status)
		return
	}
	var owner io.Closer = opened
	defer func() { _ = owner.Close() }()
	for _, name := range nativeNodeIgnoredConditions {
		r.Header.Del(name)
	}
	etag := claims.etag()
	if strings.Contains(r.Header.Get("Range"), ",") {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", claims.Size))
		w.Header().Set("ETag", etag)
		refuseNativeNodeSource(w, http.StatusRequestedRangeNotSatisfiable)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	reader, err := nativestorage.NewReadAhead(ctx, opened, nativeVideoToolReadAhead)
	if err != nil {
		refuseNativeNodeSource(w, http.StatusServiceUnavailable)
		return
	}
	owner = reader
	if err := reader.Reauthorize(ctx); err != nil {
		refuseNativeNodeSource(w, nativeNodeSourceStatus(ctx, err))
		return
	}
	reader.SetRangeEnd(nativestorage.RangeEnd(r.Header.Get("Range"), claims.Size))
	stream := httpstream.NewRollingDeadlineWriter(w)
	stream.Header().Set("Content-Type", "application/octet-stream")
	stream.Header().Set("ETag", etag)
	out := &nativeNodeSourceWriter{RollingDeadlineWriter: stream, size: claims.Size, etag: etag}
	if cfg.Lifetime != nil {
		stop := context.AfterFunc(cfg.Lifetime, func() { _ = stream.Abort() })
		defer stop()
	}
	body := &nativeEbookStream{File: reader}
	http.ServeContent(out, r.WithContext(ctx), "", time.Unix(0, claims.MTimeNS), body)
	if body.failure != nil || reader.Err() != nil || opened.Err() != nil {
		// The node's relay resumes a cut read from its offset; a short body
		// must not read as a complete one.
		panic(http.ErrAbortHandler)
	}
}

func refuseNativeNodeSource(w http.ResponseWriter, status int) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, http.StatusText(status), status)
}

// nativeNodeSourceWriter keeps the route's headers on every answer, as
// Silo's sourceResponseWriter does: no-store always, the ETag and the whole
// size on a 416, no ETag on another error.
type nativeNodeSourceWriter struct {
	*httpstream.RollingDeadlineWriter
	size int64
	etag string
}

func (w *nativeNodeSourceWriter) WriteHeader(status int) {
	header := w.Header()
	header.Set("Cache-Control", "no-store")
	switch {
	case status == http.StatusRequestedRangeNotSatisfiable:
		header.Set("Content-Range", fmt.Sprintf("bytes */%d", w.size))
		header.Set("ETag", w.etag)
	case status >= http.StatusBadRequest:
		header.Del("ETag")
	default:
		header.Set("ETag", w.etag)
	}
	w.RollingDeadlineWriter.WriteHeader(status)
}

// nativeNodeStreams counts native source responses in flight per node.
type nativeNodeStreams struct {
	mu     sync.Mutex
	active map[int]int
}

func (c *nativeNodeStreams) acquire(node, limit int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[node] >= limit {
		return false
	}
	if c.active == nil {
		c.active = make(map[int]int)
	}
	c.active[node]++
	return true
}

func (c *nativeNodeStreams) release(node int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[node]--; c.active[node] <= 0 {
		delete(c.active, node)
	}
}

// nativeVideoWorkerAdmissionV3 admits a native input only to a transcode
// node that reads its sources through the core and that the API servers can
// grant it to. A local-access node is excluded; the route loop then picks a
// core-access node or the API server as the delivery policy allows. A start
// that burns in a subtitle stays off the nodes: a core-access node refuses
// relayed text subtitle burn-in.
func (h *PlaybackHandler) nativeVideoWorkerAdmissionV3(_ context.Context, file *models.MediaFile, node *nodepool.Node, result playback.PlannerResultV3) *transportErrorV3 {
	if h.NativeVideo == nil || !NativeVideoSupported(file) {
		return nil
	}
	refuse := func(message string, cause error) *transportErrorV3 {
		return &transportErrorV3{reason: routeCapabilityUnavailableReasonV3, message: message, retryable: true, cause: cause, executorIneligible: true}
	}
	granter, ok := h.SourceGrants.(nativeNodeGranter)
	switch {
	case !ok || !granter.sources.Ready():
		return refuse("Transcode nodes cannot read this source.", errNativeNodeUnavailable)
	case node == nil || node.SourceAccess != nodepool.SourceAccessCore:
		return refuse("This transcode node reads its own library mount and cannot read this source.", errNativeNodeLocalAccess)
	case result.SubtitleBurnIn:
		return refuse("Subtitle burn-in of this source runs on the API server.", errNativeNodeLocalAccess)
	}
	return nil
}
