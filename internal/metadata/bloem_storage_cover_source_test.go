package metadata

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/artworkurl"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

type cdnArtworkResolver struct{}

func (cdnArtworkResolver) ResolveURLs(_ context.Context, keys []string) map[string]catalog.ResolvedImageURL {
	out := map[string]catalog.ResolvedImageURL{}
	for _, key := range keys {
		out[key] = catalog.ResolvedImageURL{URL: "https://cdn.example/" + key}
	}
	return out
}

func TestStorageCoversResolveToTheSignedServerRoute(t *testing.T) {
	r := NewPluginImageResolver()
	t.Cleanup(r.Close)
	signer := artworkurl.NewSigner("test-secret", time.Hour)
	// Other artwork is delivered from a CDN; storage covers never are.
	r.SetArtworkResolver(cdnArtworkResolver{})
	r.ReplaceSources([]PluginImageResolverSourceRegistration{StorageCoverRegistration(NewStorageCoverSource(artworkurl.NewServerResolver(signer)))})
	cover := artworkkey.StorageCoverPath("146532612416483348", "cover/book-1", "cover:abc")
	stored := "local/ebooks/1/poster/original.r1.webp"
	got := r.ResolveImageURLs(t.Context(), []string{cover, stored, "bloem-storage://not-a-cover"}, "w300")
	key, ok := signer.SignedKey(got[cover], time.Now())
	if !ok || key != artworkkey.StorageCoverKey("146532612416483348", "cover/book-1", "cover:abc") {
		t.Fatalf("cover URL = %q", got[cover])
	}
	if !strings.HasPrefix(got[stored], "https://cdn.example/") {
		t.Fatalf("stored URL = %q", got[stored])
	}
	if got["bloem-storage://not-a-cover"] != "" {
		t.Fatalf("invalid storage path resolved: %q", got["bloem-storage://not-a-cover"])
	}
}
