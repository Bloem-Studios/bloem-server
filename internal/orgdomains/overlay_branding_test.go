package orgdomains

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"

	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/branding"
)

type memoryAssets struct{ objects map[string][]byte }

func (m *memoryAssets) Put(_ context.Context, key string, data []byte) error {
	m.objects[key] = append([]byte(nil), data...)
	return nil
}

func (m *memoryAssets) Get(_ context.Context, key string) (io.ReadCloser, blobstore.ObjectInfo, error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, blobstore.ObjectInfo{}, blobstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), blobstore.ObjectInfo{Size: int64(len(data))}, nil
}

func (m *memoryAssets) Stat(_ context.Context, key string) (blobstore.ObjectInfo, error) {
	data, ok := m.objects[key]
	if !ok {
		return blobstore.ObjectInfo{}, blobstore.ErrNotFound
	}
	return blobstore.ObjectInfo{Size: int64(len(data))}, nil
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		for y := 0; y < 64; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 40, B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Silo's branding service, built over the overlay, serves each organization's
// own name and mark, and an organization's upload never becomes the server's.
func TestBrandingServiceThroughOverlay(t *testing.T) {
	live := newLiveDirectory()
	server := newMemorySettings(map[string]string{branding.KeyServerName: "Strm"})
	svc := branding.NewService(newSettingsOverlay(server, live.d, live, OverlayOptions{}), &memoryAssets{objects: map[string][]byte{}})

	ref, err := svc.UploadAsset(WithBrandingWrite(context.Background(), testOrg2), branding.KindMark, testPNG(t), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if v := server.values; v["branding.mark_ref"] != "" {
		t.Fatalf("the server's mark became %q", v["branding.mark_ref"])
	}

	org2 := svc.Load(onHost(testOrg2))
	if org2.ServerName != "Main" {
		t.Fatalf("org 2 name = %q, want Main", org2.ServerName)
	}
	if _, _, got, err := svc.GetAsset(onHost(testOrg2), branding.KindMark); err != nil || got != ref {
		t.Fatalf("org 2 mark = %q, %v; want %q", got, err, ref)
	}
	if def := svc.Load(context.Background()); def.ServerName != "Strm" {
		t.Fatalf("default host name = %q, want the server's", def.ServerName)
	}
	if _, _, _, err := svc.GetAsset(context.Background(), branding.KindMark); !errors.Is(err, branding.ErrAssetNotConfigured) {
		t.Fatalf("default host mark: %v, want none", err)
	}
	if err := svc.DeleteAsset(WithBrandingWrite(context.Background(), testOrg2), branding.KindMark); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.GetAsset(onHost(testOrg2), branding.KindMark); !errors.Is(err, branding.ErrAssetNotConfigured) {
		t.Fatalf("org 2 mark after delete: %v, want none", err)
	}
}
