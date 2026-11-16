package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nodecontrol"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/nodesource"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// nativeNodeKeys is the key source of the fixture's transcode nodes.
type nativeNodeKeys map[int]string

func (k nativeNodeKeys) NodeKey(id int) (string, error) {
	if key, ok := k[id]; ok {
		return key, nil
	}
	return "", nodepool.ErrNodeNotFound
}
func (k nativeNodeKeys) ReloadNodeKey(id int) (string, error) { return k.NodeKey(id) }

// nativeNodeFixture is a stored movie bound to a playback session, and a
// core serving node 7 (core source access) and node 8 (local source access)
// through Bloem's handlers in front of stand-ins for Silo's source routes.
type nativeNodeFixture struct {
	service      *NativeVideoService
	file         *models.MediaFile
	session      *playback.Session
	sources      *NativeNodeSources
	granter      nodesource.FileGranter
	core         *httptest.Server
	keys         nativeNodeKeys
	siloOpens    *atomic.Int32
	siloReads    *atomic.Int32
	openFailures error
}

func newNativeNodeFixture(t *testing.T) *nativeNodeFixture {
	t.Helper()
	s, file, session, _, _ := nativeVideoFixture(t)
	s.Bindings = NewNativeVideoBindingStore(nil)
	f := &nativeNodeFixture{service: s, file: file, session: session, siloOpens: &atomic.Int32{}, siloReads: &atomic.Int32{}}
	s.OpenDelegated = func(ctx context.Context, requested *models.MediaFile, binding NativeVideoBinding) (NativeVideoFile, error) {
		if f.openFailures != nil {
			return nil, f.openFailures
		}
		if binding.SessionID != session.ID {
			return nil, errors.New("wrong binding")
		}
		return s.Open(ctx, requested)
	}
	if err := s.Bind(t.Context(), session, file); err != nil {
		t.Fatal(err)
	}
	nodes := map[int]*nodepool.Node{
		7: {ID: 7, Type: "transcode", SourceAccess: nodepool.SourceAccessCore},
		8: {ID: 8, Type: "transcode", SourceAccess: nodepool.SourceAccessLocal},
	}
	f.sources = &NativeNodeSources{}
	f.sources.Install(NativeNodeSourceConfig{Service: s, Secret: func() string { return "fixture-secret" }, Lifetime: t.Context(),
		Node: func(_ context.Context, id int) (*nodepool.Node, error) {
			if node, ok := nodes[id]; ok {
				return node, nil
			}
			return nil, nodepool.ErrNodeNotFound
		}})
	f.granter = f.sources.Granter(siloGranterFixture{})
	f.keys = nativeNodeKeys{}
	for _, id := range []int{7, 8} {
		key, err := nodepool.GenerateNodeKey(id)
		if err != nil {
			t.Fatal(err)
		}
		f.keys[id] = key
	}
	// The routes as the router mounts them: the source bytes middleware in
	// front of everything, the /self middleware after Silo's node
	// authentication. The Silo handlers here only count what reaches them.
	mux := http.NewServeMux()
	silo := func(count *atomic.Int32) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			count.Add(1)
			http.Error(w, "silo", http.StatusTeapot)
		})
	}
	mux.Handle(nodesource.PathSources, nodecontrol.RequireNode(f.keys)(silo(f.siloReads)))
	mux.Handle(pathSourceOpen, nodecontrol.RequireNode(f.keys)(f.sources.NodeSelfMiddleware(nil)(silo(f.siloOpens))))
	f.core = httptest.NewServer(f.sources.SourceBytesMiddleware(f.keys)(mux))
	t.Cleanup(f.core.Close)
	return f
}

// siloGranterFixture stands in for Silo's grants: it names the file.
type siloGranterFixture struct{}

func (siloGranterFixture) ForFile(_ context.Context, nodeID int, file *models.MediaFile) (string, error) {
	return fmt.Sprintf("silo-%d-%d", nodeID, file.ID), nil
}

func (f *nativeNodeFixture) grant(t *testing.T, nodeID int) string {
	t.Helper()
	grant, err := f.granter.ForFile(WithNativeNodeGrantSession(t.Context(), f.session.ID), nodeID, f.file)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (f *nativeNodeFixture) client(t *testing.T, nodeID int) *nodecontrol.Client {
	t.Helper()
	client, err := nodecontrol.NewClient(f.core.URL, f.keys[nodeID])
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func readRange(t *testing.T, client *nodecontrol.Client, read nodesource.ReadRequest) (int, string, http.Header) {
	t.Helper()
	resp, err := client.ReadSource(t.Context(), read)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

// A Bloem grant for a native file is exchanged at sources/open for an opaque
// path and the revision, and its bytes are served through the source bytes
// route with Silo's semantics; Silo's handlers never see it.
func TestNativeNodeGrantOpensAndServesBytes(t *testing.T) {
	f := newNativeNodeFixture(t)
	grant := f.grant(t, 7)
	if !IsNativeNodeGrant(grant) || strings.Contains(grant, "movie") {
		t.Fatalf("grant %q is not a sealed Bloem grant", grant)
	}
	client := f.client(t, 7)
	opened, err := client.OpenSource(t.Context(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Size != 10 || !strings.HasPrefix(opened.Path, "/bloem-native/42/") || strings.Contains(opened.Path, "bloem-storage") || opened.ETag == "" || !IsNativeNodeGrant(opened.Grant) {
		t.Fatalf("open answer = %+v", opened)
	}
	read := nodesource.ReadRequest{Grant: opened.Grant, ETag: opened.ETag, Offset: 3, Length: 4}
	status, body, header := readRange(t, client, read)
	if status != http.StatusPartialContent || body != "3456" || header.Get("ETag") != opened.ETag || header.Get("Cache-Control") != "no-store" || header.Get("Content-Range") != "bytes 3-6/10" {
		t.Fatalf("range read = %d %q %v", status, body, header)
	}
	status, _, header = readRange(t, client, nodesource.ReadRequest{Grant: opened.Grant, ETag: opened.ETag, Offset: 20, Length: 4})
	if status != http.StatusRequestedRangeNotSatisfiable || header.Get("Content-Range") != "bytes */10" || header.Get("ETag") != opened.ETag || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("range past the end = %d %v", status, header)
	}
	status, _, header = readRange(t, client, nodesource.ReadRequest{Grant: opened.Grant, ETag: `"other"`, Offset: 0, Length: 4})
	if status != http.StatusPreconditionFailed || header.Get("ETag") != "" || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("If-Match on another revision = %d %v", status, header)
	}
	if f.siloOpens.Load() != 0 || f.siloReads.Load() != 0 {
		t.Fatalf("a Bloem grant reached Silo's handlers: opens=%d reads=%d", f.siloOpens.Load(), f.siloReads.Load())
	}
	// A stopped playback revokes its binding: the next open and read refuse.
	if err := f.service.Bindings.RevokeSession(t.Context(), f.session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.OpenSource(t.Context(), opened.Grant); openStatus(err) != http.StatusForbidden {
		t.Fatalf("open after the playback stopped = %v, want 403", err)
	}
	if status, _, _ := readRange(t, client, read); status != http.StatusForbidden {
		t.Fatalf("read after the playback stopped = %d, want 403", status)
	}
}

// Silo's own node-side opener and loopback relay read a native source
// through the core, as ffmpeg on a core-access node does.
func TestNativeNodeSourceThroughSilosOpenerAndRelay(t *testing.T) {
	f := newNativeNodeFixture(t)
	client := f.client(t, 7)
	opener := nodesource.NewOpener(client.OpenSource, slog.New(slog.DiscardHandler))
	opener.EnableCoreAccess(nodesource.CoreAccess{Fetch: client.ReadSource, Mode: func() string { return nodesource.ModeCore }})
	t.Cleanup(func() { _ = opener.Close() })
	source, err := opener.Open(t.Context(), f.grant(t, 7))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	if !nodesource.IsRelayURL(source.Input()) || source.Size() != 10 {
		t.Fatalf("source input %q size %d, want a relay URL of 10 bytes", source.Input(), source.Size())
	}
	for _, rangeHeader := range []string{"", "bytes=4-"} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, source.Input(), nil)
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		want := "0123456789"
		if rangeHeader != "" {
			want = "456789"
		}
		if string(body) != want {
			t.Fatalf("relay read %q = %d %q, want %q", rangeHeader, resp.StatusCode, body, want)
		}
	}
}

// A native input never goes to a node that reads its own library mount: no
// grant is minted for it, a grant for another node is refused, and the
// playback admission excludes it while admitting a core-access node.
func TestNativeNodeRefusesALocalAccessNode(t *testing.T) {
	f := newNativeNodeFixture(t)
	if _, err := f.granter.ForFile(WithNativeNodeGrantSession(t.Context(), f.session.ID), 8, f.file); !errors.Is(err, errNativeNodeLocalAccess) {
		t.Fatalf("grant for a local-access node = %v, want errNativeNodeLocalAccess", err)
	}
	if _, err := f.granter.ForFile(t.Context(), 7, f.file); !errors.Is(err, errNativeNodeNoSession) {
		t.Fatalf("grant without a playback = %v, want errNativeNodeNoSession", err)
	}
	grant := f.grant(t, 7)
	if _, err := f.client(t, 8).OpenSource(t.Context(), grant); openStatus(err) != http.StatusForbidden {
		t.Fatalf("node 8 presenting node 7's grant = %v, want 403", err)
	}
	h := &PlaybackHandler{NativeVideo: f.service, SourceGrants: f.granter}
	result := playback.PlannerResultV3{}
	if failure := h.nativeVideoWorkerAdmissionV3(t.Context(), f.file, &nodepool.Node{ID: 8, SourceAccess: nodepool.SourceAccessLocal}, result); failure == nil || !failure.executorIneligible || !errors.Is(failure.cause, errNativeNodeLocalAccess) {
		t.Fatalf("local-access node admission = %+v, want an ineligible executor", failure)
	}
	if failure := h.nativeVideoWorkerAdmissionV3(t.Context(), f.file, &nodepool.Node{ID: 7, SourceAccess: nodepool.SourceAccessCore}, result); failure != nil {
		t.Fatalf("core-access node admission = %+v, want admitted", failure)
	}
	result.SubtitleBurnIn = true
	if failure := h.nativeVideoWorkerAdmissionV3(t.Context(), f.file, &nodepool.Node{ID: 7, SourceAccess: nodepool.SourceAccessCore}, result); failure == nil || !failure.executorIneligible {
		t.Fatalf("burn-in admission = %+v, want an ineligible executor", failure)
	}
	h.SourceGrants = siloGranterFixture{}
	if failure := h.nativeVideoWorkerAdmissionV3(t.Context(), f.file, &nodepool.Node{ID: 7, SourceAccess: nodepool.SourceAccessCore}, playback.PlannerResultV3{}); failure == nil {
		t.Fatal("a node was admitted without Bloem's granter")
	}
}

// Ordinary files keep Silo's grants and Silo's handlers; a Silo grant can
// never open a native file.
func TestNativeNodeLeavesSiloGrantsToSilo(t *testing.T) {
	f := newNativeNodeFixture(t)
	ordinary := &models.MediaFile{ID: 9, FilePath: "/media/movies/ordinary.mkv"}
	grant, err := f.granter.ForFile(t.Context(), 8, ordinary)
	if err != nil || grant != "silo-8-9" {
		t.Fatalf("ordinary grant = %q, %v; want Silo's", grant, err)
	}
	client := f.client(t, 8)
	if _, err := client.OpenSource(t.Context(), grant); openStatus(err) != http.StatusTeapot {
		t.Fatalf("Silo grant open = %v, want Silo's handler", err)
	}
	if status, _, _ := readRange(t, client, nodesource.ReadRequest{Grant: grant, ETag: `"x"`, Offset: 0, Length: 1}); status != http.StatusTeapot {
		t.Fatalf("Silo grant read = %d, want Silo's handler", status)
	}
	if f.siloOpens.Load() != 1 || f.siloReads.Load() != 1 {
		t.Fatalf("Silo's handlers saw opens=%d reads=%d, want 1 and 1", f.siloOpens.Load(), f.siloReads.Load())
	}
	catalog := f.sources.FileCatalog(activeFiles{})
	if active, _ := catalog.IsActiveFile(t.Context(), 41, "/media/movies/Movie.strm"); active {
		t.Fatal("a Silo grant could open a .strm file")
	}
	if active, _ := catalog.IsActiveFile(t.Context(), 9, ordinary.FilePath); !active {
		t.Fatal("an ordinary file is no longer active for Silo")
	}
	resolving := context.WithValue(t.Context(), nativeNodeResolveKey{}, "transport")
	if active, _ := catalog.IsActiveFile(resolving, 41, "/media/movies/Movie.strm"); !active {
		t.Fatal("a resolve of a native recipe cannot find its row")
	}
}

type activeFiles struct{}

func (activeFiles) IsActiveFile(context.Context, int, string) (bool, error) { return true, nil }

// openStatus is the status a failed sources/open answered with.
func openStatus(err error) int {
	var status *nodesource.StatusError
	if errors.As(err, &status) {
		return status.Status
	}
	return 0
}
