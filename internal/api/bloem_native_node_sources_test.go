package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nodecontrol"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/nodesource"
	"github.com/Silo-Server/silo-server/internal/remotesource"
)

type strmNodeKeys map[int]string

func (k strmNodeKeys) NodeKey(id int) (string, error) {
	if key, ok := k[id]; ok {
		return key, nil
	}
	return "", nodepool.ErrNodeNotFound
}
func (k strmNodeKeys) ReloadNodeKey(id int) (string, error) { return k.NodeKey(id) }

type strmNodeFiles struct{ file *models.MediaFile }

func (f strmNodeFiles) GetByID(context.Context, int) (*models.MediaFile, error) {
	current := *f.file
	return &current, nil
}

type silosGrants struct{}

func (silosGrants) ForFile(context.Context, int, *models.MediaFile) (string, error) {
	return "silo", nil
}

// A `.strm` item reads through a core-access node's grant under the `.strm`
// security default: with private addresses allowed its bytes are served, and
// once the guard refuses the address, sources/open and the bytes route
// answer 403 without naming it.
func TestStrmNodeSourceKeepsTheAddressGuard(t *testing.T) {
	server := strmMediaServer(t, map[string][]byte{"/one.mkv": strmBlob(4096)})
	f := newStrmFixture(t, true)
	f.sources.put(f.file.FilePath, server.URL+"/one.mkv?token=very-secret", "")
	f.service.Files = strmNodeFiles{file: f.file}
	f.service.OpenDelegated = func(ctx context.Context, file *models.MediaFile, _ handlers.NativeVideoBinding) (handlers.NativeVideoFile, error) {
		return f.service.Open(ctx, file)
	}
	if err := f.service.Bind(t.Context(), f.session, f.file); err != nil {
		t.Fatal(err)
	}
	key, err := nodepool.GenerateNodeKey(7)
	if err != nil {
		t.Fatal(err)
	}
	keys := strmNodeKeys{7: key}
	sources := &handlers.NativeNodeSources{}
	sources.Install(handlers.NativeNodeSourceConfig{Service: f.service, Secret: func() string { return "fixture-secret" }, Lifetime: t.Context(),
		Node: func(context.Context, int) (*nodepool.Node, error) {
			return &nodepool.Node{ID: 7, SourceAccess: nodepool.SourceAccessCore}, nil
		}})
	grant, err := sources.Granter(silosGrants{}).ForFile(handlers.WithNativeNodeGrantSession(t.Context(), f.session.ID), 7, f.file)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/internal/nodes/self/sources/open", nodecontrol.RequireNode(keys)(sources.NodeSelfMiddleware(nil)(http.NotFoundHandler())))
	core := httptest.NewServer(sources.SourceBytesMiddleware(keys)(mux))
	t.Cleanup(core.Close)
	client, err := nodecontrol.NewClient(core.URL, key)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := client.OpenSource(t.Context(), grant)
	if err != nil {
		t.Fatal(err)
	}
	read := nodesource.ReadRequest{Grant: opened.Grant, ETag: opened.ETag, Offset: 100, Length: 50}
	resp, err := client.ReadSource(t.Context(), read)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != string(strmBlob(4096)[100:150]) {
		t.Fatalf("allowed read = %d, %d bytes", resp.StatusCode, len(body))
	}

	// The private-address setting is turned off: every fetch is checked again.
	f.playback.guard = remotesource.NewGuard(remotesource.Options{AllowPrivate: func(context.Context) bool { return false }})
	_, err = client.OpenSource(t.Context(), opened.Grant)
	if openStatus(err) != http.StatusForbidden {
		t.Fatalf("open of a refused address = %v, want 403", err)
	}
	resp, err = client.ReadSource(t.Context(), read)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read of a refused address = %d, want 403", resp.StatusCode)
	}
	host := strings.TrimPrefix(server.URL, "http://")
	for _, text := range []string{string(body), err2text(err), opened.Path, opened.ETag, grant} {
		if strings.Contains(text, "very-secret") || strings.Contains(text, host) || strings.Contains(text, "127.0.0.1") {
			t.Fatalf("a node-facing answer names the address: %q", text)
		}
	}
}

func err2text(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// openStatus is the status a failed sources/open answered with.
func openStatus(err error) int {
	var status *nodesource.StatusError
	if errors.As(err, &status) {
		return status.Status
	}
	return 0
}
