// Synthetic, finite provider for consumer integration tests only.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"runtime"
	"strconv"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	sdkruntime "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type object struct {
	entry *storagev1.Entry
	data  []byte
}
type server struct {
	publicv1.UnimplementedRuntimeServer
	storagev1.UnimplementedStorageProviderServer
	manifest     *publicv1.PluginManifest
	objects      []object
	mode, notify string
	pinned       bool
}

func (s *server) GetManifest(context.Context, *publicv1.GetManifestRequest) (*publicv1.GetManifestResponse, error) {
	return &publicv1.GetManifestResponse{Manifest: s.manifest}, nil
}
func (s *server) Configure(_ context.Context, r *publicv1.ConfigureRequest) (*publicv1.ConfigureResponse, error) {
	if len(r.Config) != 1 || r.Config[0].Key != "source" {
		return nil, status.Error(codes.InvalidArgument, "source required")
	}
	f := r.Config[0].Value.GetFields()
	s.mode = f["mode"].GetStringValue()
	s.notify = f["notify"].GetStringValue()
	if _, err := os.Stat(s.notify + ".offline"); err == nil {
		return nil, status.Error(codes.Unavailable, "owned provider offline")
	}
	s.pinned = s.mode != "false"
	revision := f["revision"].GetStringValue()
	if revision == "" {
		revision = "v1"
	}
	s.objects = fixtures(revision)
	if s.mode == "roots" {
		epub := s.objects[0].data
		s.objects = nil
		for _, root := range []struct{ id, prefix, title string }{{"bare", "", "Bare Root"}, {"slash", "/", "Slash Root"}, {"double", "//", "Double Root"}} {
			entry := &storagev1.Entry{Id: map[string]string{"bare": "01-bare", "slash": "02-slash", "double": "03-double"}[root.id], Name: "Book.epub", LogicalPath: root.prefix + "Book.epub", Size: int64(len(epub)), Revision: revision, Kind: storagev1.EntryKind_ENTRY_KIND_FILE}
			s.objects = append(s.objects, object{entry, epub})
			opf := []byte(fmt.Sprintf("<package><metadata><title>%s</title><creator>Root Writer</creator></metadata></package>", root.title))
			s.objects = append(s.objects, object{&storagev1.Entry{Id: "04-opf-" + root.id, Name: "Book.opf", LogicalPath: root.prefix + "Book.opf", Size: int64(len(opf)), Revision: revision, Kind: storagev1.EntryKind_ENTRY_KIND_FILE}, opf})
		}
	}
	if s.mode == "multibook" {
		s.objects[len(s.objects)-1].entry.Name = "other.fb2.zip"
		s.objects[len(s.objects)-1].entry.LogicalPath = "Books//../one/other.fb2.zip"
	}
	if s.mode == "unsupported" {
		s.objects = s.objects[len(s.objects)-1:]
	}
	return &publicv1.ConfigureResponse{}, nil
}
func (s *server) Describe(context.Context, *storagev1.DescribeRequest) (*storagev1.DescribeResponse, error) {
	return &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: "books", RootEntryId: "root", RevisionPinnedReads: s.pinned}}}, nil
}
func (s *server) audit(kind string) {
	if s.notify == "" {
		return
	}
	f, err := os.OpenFile(s.notify+".io", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		_, _ = fmt.Fprintln(f, kind)
		_ = f.Close()
	}
}
func (s *server) signal(kind string) {
	if s.notify != "" {
		_ = os.WriteFile(s.notify, []byte(kind), 0600)
	}
}
func (s *server) List(ctx context.Context, r *storagev1.ListRequest) (*storagev1.ListResponse, error) {
	s.audit("list")
	if r.SourceId != "books" || r.DirectoryId != "root" || r.MaxEntries <= 0 {
		return nil, status.Error(codes.InvalidArgument, "wrong list scope")
	}
	if s.mode == "listblock" {
		s.signal("list")
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	offset := 0
	if r.Cursor != "" {
		var err error
		offset, err = strconv.Atoi(r.Cursor)
		if err != nil || offset < 0 || offset >= len(s.objects) {
			return nil, status.Error(codes.InvalidArgument, "cursor")
		}
	}
	end := min(offset+2, len(s.objects))
	page := &storagev1.ListResponse{Complete: end == len(s.objects)}
	for _, o := range s.objects[offset:end] {
		page.Entries = append(page.Entries, o.entry)
	}
	if !page.Complete {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}
func (s *server) find(source, id, rev string) (object, error) {
	if source != "books" {
		return object{}, status.Error(codes.PermissionDenied, "wrong source")
	}
	for _, o := range s.objects {
		if o.entry.Id == id {
			if o.entry.Revision != rev {
				return object{}, status.Error(codes.FailedPrecondition, "revision changed")
			}
			return o, nil
		}
	}
	return object{}, status.Error(codes.NotFound, "unknown entry")
}
func (s *server) Stat(_ context.Context, r *storagev1.StatRequest) (*storagev1.StatResponse, error) {
	s.audit("stat " + r.EntryId)
	o, err := s.find(r.SourceId, r.EntryId, r.ExpectedRevision)
	if err != nil {
		return nil, err
	}
	return &storagev1.StatResponse{Entry: o.entry}, nil
}
func (s *server) Read(r *storagev1.ReadRequest, out grpc.ServerStreamingServer[storagev1.ReadChunk]) error {
	s.audit("read " + r.EntryId)
	o, err := s.find(r.SourceId, r.EntryId, r.ExpectedRevision)
	if err != nil {
		return err
	}
	if s.mode == "readblock" {
		s.signal("read")
		<-out.Context().Done()
		return status.FromContextError(out.Context().Err()).Err()
	}
	if s.mode == "crash" {
		s.signal("crash")
		os.Exit(7)
	}
	if s.mode == "denied" {
		return status.Error(codes.PermissionDenied, "synthetic denied")
	}
	if r.Offset < 0 || r.Length <= 0 || r.Offset+r.Length > int64(len(o.data)) {
		return status.Error(codes.OutOfRange, "range")
	}
	end := r.Offset + r.Length
	if s.mode == "late" {
		if err = out.Send(&storagev1.ReadChunk{Offset: r.Offset, Data: o.data[r.Offset:end], Eof: true}); err != nil {
			return err
		}
		return status.Error(codes.Unavailable, "late pinned read error")
	}
	for off := r.Offset; off < end; {
		next := min(off+64<<10, end)
		if err = out.Send(&storagev1.ReadChunk{Offset: off, Data: o.data[off:next], Eof: next == end}); err != nil {
			return err
		}
		off = next
	}
	return nil
}
func fixtures(revision string) []object {
	var epub bytes.Buffer
	z := zip.NewWriter(&epub)
	for _, f := range []struct{ name, data string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?><container><rootfiles><rootfile full-path="OPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"OPS/book.opf", `<package><metadata><title>Embedded EPUB</title><creator>Ada Writer</creator><language>en</language><date>2024-01-02</date></metadata></package>`},
	} {
		w, _ := z.Create(f.name)
		_, _ = w.Write([]byte(f.data))
	}
	_ = z.Close()
	pdf := []byte("%PDF-1.7\n1 0 obj\n<< /Title (Native PDF) /Author (Ben Writer) /CreationDate (D:20250102000000Z) >>\nendobj\n")
	xref := len(pdf)
	offset := bytes.Index(pdf, []byte("1 0 obj"))
	pdf = fmt.Appendf(pdf, "xref\n0 2\n0000000000 65535 f \n%010d 00000 n \ntrailer\n<< /Size 2 >>\nstartxref\n%d\n%%%%EOF\n", offset, xref)
	var cover bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{100, 50, uint8(x * 10), 255})
		}
	}
	_ = png.Encode(&cover, img)
	data := []struct {
		id, name, path string
		data           []byte
	}{
		{"01-epub", "book.epub", "Books//../one/book.epub", epub.Bytes()},
		{"02-pdf", "report.pdf", "Books/two/report.pdf", pdf},
		{"03-opf", "book.opf", "Books//../one/book.opf", []byte(`<package><metadata><title>Sidecar EPUB</title><creator>Sidecar Writer</creator><language>nl</language><date>2026-02-03</date></metadata></package>`)},
		{"04-cover", "cover.png", "Books//../one/cover.png", cover.Bytes()},
		{"05-mobi", "other.mobi", "Books/three/other.mobi", []byte("unsupported fixture")},
	}
	var result []object
	for _, f := range data {
		result = append(result, object{&storagev1.Entry{Id: f.id, Name: f.name, LogicalPath: f.path, Size: int64(len(f.data)), Revision: revision, Kind: storagev1.EntryKind_ENTRY_KIND_FILE, ModifiedUnixNano: 1700000000000000000}, f.data})
	}
	return result
}

type extension struct {
	*sdkruntime.GRPCPlugin
	storage storagev1.StorageProviderServer
}

func (e *extension) GRPCServer(b *plugin.GRPCBroker, s *grpc.Server) error {
	if err := e.GRPCPlugin.GRPCServer(b, s); err != nil {
		return err
	}
	storagev1.RegisterStorageProviderServer(s, e.storage)
	return nil
}
func main() {
	path, _ := os.Executable()
	binary, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(binary)
	s := &server{manifest: &publicv1.PluginManifest{PluginId: "bloem.consumer.fixture", Version: "1.0.0", SiloApiVersion: "v1", Checksum: hex.EncodeToString(sum[:]), SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}}
	sdkruntime.Serve(sdkruntime.ServeConfig{Plugins: plugin.PluginSet{sdkruntime.PluginSetName: &extension{GRPCPlugin: &sdkruntime.GRPCPlugin{Servers: sdkruntime.CapabilityServers{Runtime: s}}, storage: s}}})
}
