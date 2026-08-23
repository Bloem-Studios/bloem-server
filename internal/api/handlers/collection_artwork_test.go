package handlers

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/Silo-Server/silo-server/internal/outbound"
	"github.com/Silo-Server/silo-server/internal/s3client"
)

type collectionArtworkResolver map[string][]netip.Addr

func (r collectionArtworkResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), r[host]...), nil
}

func TestDownloadCollectionImageURLRejectsPrivateDestinations(t *testing.T) {
	client := outbound.NewClient(
		outbound.PublicHTTPPolicy(),
		outbound.WithResolver(collectionArtworkResolver{
			"private.example": {netip.MustParseAddr("10.0.0.7")},
		}),
	)
	_, err := downloadCollectionImageURL(t.Context(), client, "http://private.example/poster.jpg")
	if err == nil {
		t.Fatal("private collection artwork source was accepted")
	}
}

func TestCreatePersonalCollectionReportsPrivateArtworkAsBadRequest(t *testing.T) {
	store := newPlaybackTestStore(t)
	handler := NewCollectionHandler(testUserStoreProvider{store: store})
	handler.ArtworkClient = outbound.NewClient(
		outbound.PublicHTTPPolicy(),
		outbound.WithResolver(collectionArtworkResolver{
			"private.example": {netip.MustParseAddr("10.0.0.7")},
		}),
	)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/collections", strings.NewReader(`{
		"name":"Private artwork",
		"collection_type":"manual",
		"poster_source_url":"http://private.example/poster.jpg"
	}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(newAuthorizedPlaybackContext())
	response := httptest.NewRecorder()

	handler.HandleCreateCollection(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"error":"bad_request"`) {
		t.Fatalf("body = %s, want typed bad_request", response.Body.String())
	}
}

func TestReadCollectionImageMultipartRejectsUndecodableJPEG(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="poster"; filename="poster.jpg"`)
	header.Set("Content-Type", "image/jpeg")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("not actually a jpeg"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/collections", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	if _, err := readCollectionImageMultipart(request, "poster"); err == nil {
		t.Fatal("undecodable JPEG was accepted")
	}
}

type collectionArtworkS3Recorder struct {
	server *httptest.Server
	mu     sync.Mutex
	puts   []string
}

func newCollectionArtworkS3Recorder(t *testing.T) *collectionArtworkS3Recorder {
	t.Helper()

	recorder := &collectionArtworkS3Recorder{}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		if r.Method == http.MethodPut {
			recorder.mu.Lock()
			recorder.puts = append(recorder.puts, r.URL.Path)
			recorder.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(recorder.server.Close)
	return recorder
}

func (r *collectionArtworkS3Recorder) client() *s3client.Client {
	return s3client.NewClient(s3client.BucketConfig{
		Endpoint:  r.server.URL,
		Region:    "us-east-1",
		Bucket:    "public-assets",
		AccessKey: "test",
		SecretKey: "test",
		PathStyle: true,
	})
}

func (r *collectionArtworkS3Recorder) putPaths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.puts))
	copy(out, r.puts)
	return out
}

func TestStoreBundledCollectionPosterIfS3Configured_NoS3KeepsPath(t *testing.T) {
	path := "/images/collection-templates/template.jpg"
	gotPath, gotThumbhash, stored, err := storeBundledCollectionPosterIfS3Configured(
		context.Background(),
		nil,
		fstest.MapFS{},
		"collection-1",
		adminCollectionImagePrefix,
		path,
	)
	if err != nil {
		t.Fatalf("storeBundledCollectionPosterIfS3Configured: %v", err)
	}
	if stored {
		t.Fatal("stored = true, want false")
	}
	if gotPath != path {
		t.Fatalf("path = %q, want %q", gotPath, path)
	}
	if gotThumbhash != "" {
		t.Fatalf("thumbhash = %q, want empty", gotThumbhash)
	}
}

func TestStoreBundledCollectionPosterIfS3Configured_IgnoresNonTemplatePath(t *testing.T) {
	recorder := newCollectionArtworkS3Recorder(t)
	path := "collection-images/existing/poster/original.webp"

	gotPath, gotThumbhash, stored, err := storeBundledCollectionPosterIfS3Configured(
		context.Background(),
		recorder.client(),
		fstest.MapFS{},
		"collection-1",
		adminCollectionImagePrefix,
		path,
	)
	if err != nil {
		t.Fatalf("storeBundledCollectionPosterIfS3Configured: %v", err)
	}
	if stored {
		t.Fatal("stored = true, want false")
	}
	if gotPath != path {
		t.Fatalf("path = %q, want %q", gotPath, path)
	}
	if gotThumbhash != "" {
		t.Fatalf("thumbhash = %q, want empty", gotThumbhash)
	}
	if puts := recorder.putPaths(); len(puts) != 0 {
		t.Fatalf("PUT paths = %#v, want none", puts)
	}
}

func TestStoreBundledCollectionPosterIfS3Configured_UploadsTemplatePoster(t *testing.T) {
	recorder := newCollectionArtworkS3Recorder(t)
	frontendFS := fstest.MapFS{
		"images/collection-templates/template.jpg": {
			Data: testCollectionPosterJPEG(t),
		},
	}

	gotPath, gotThumbhash, stored, err := storeBundledCollectionPosterIfS3Configured(
		context.Background(),
		recorder.client(),
		frontendFS,
		"collection-1",
		adminCollectionImagePrefix,
		"/images/collection-templates/template.jpg",
	)
	if err != nil {
		t.Fatalf("storeBundledCollectionPosterIfS3Configured: %v", err)
	}
	if !stored {
		t.Fatal("stored = false, want true")
	}
	if gotPath != "collection-images/collection-1/poster/original.webp" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotThumbhash == "" {
		t.Fatal("thumbhash is empty")
	}

	want := map[string]bool{
		"/public-assets/collection-images/collection-1/poster/original.webp": true,
		"/public-assets/collection-images/collection-1/poster/w500.webp":     true,
		"/public-assets/collection-images/collection-1/poster/w300.webp":     true,
	}
	puts := recorder.putPaths()
	if len(puts) != len(want) {
		t.Fatalf("PUT paths = %#v", puts)
	}
	for _, path := range puts {
		if !want[path] {
			t.Fatalf("unexpected PUT path %q in %#v", path, puts)
		}
	}
}

func testCollectionPosterJPEG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 32, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 6), G: uint8(y * 4), B: 120, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}
