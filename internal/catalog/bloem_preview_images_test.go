package catalog

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

type previewImageRecorder struct{ calls []string }

func (r *previewImageRecorder) ResolveImageURL(_ context.Context, p, v string) string {
	r.calls = append(r.calls, p)
	return "signed:" + p
}
func (r *previewImageRecorder) ResolveImageURLs(ctx context.Context, ps []string, v string) map[string]string {
	out := map[string]string{}
	for _, p := range ps {
		out[p] = r.ResolveImageURL(ctx, p, v)
	}
	return out
}
func TestPreviewArtworkDoesNotMintFileFrames(t *testing.T) {
	next := &previewImageRecorder{}
	resolver := BloemPreviewImages(next, nil)
	ctx := previewcontext.With(t.Context())
	for _, p := range []string{"chapter-images/7/0/w300.webp", "chapter-images/7/marker-intro/w300.webp", "chapters/7/w300.webp", "markers/7/frame.webp", "trickplay/7/0.jpg", "local/item/still/original.webp"} {
		if resolver.ResolveImageURL(ctx, p, "card") != "" {
			t.Fatalf("minted %s", p)
		}
	}
	if len(next.calls) != 0 {
		t.Fatal("file frames reached signer")
	}
	if resolver.ResolveImageURL(t.Context(), "chapters/7/w300.webp", "card") == "" {
		t.Fatal("subscriber frame lost")
	}
}
