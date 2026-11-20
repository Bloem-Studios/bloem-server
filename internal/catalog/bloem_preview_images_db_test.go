package catalog

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

// A preview shows the artwork a library supplies (sidecar backdrops, logos and
// episode thumbnails are cached under local/ keys) and refuses only the frames
// Bloem took from a video itself, which an extra's still source records.
func TestPreviewArtworkKeepsLibraryArtworkAndRefusesTakenFrames(t *testing.T) {
	pool := newTestPool(t)
	ctx := t.Context()
	p := fmt.Sprintf("prevart%d", time.Now().UnixNano())
	movie, sidecarExtra, frameExtra := p+"-movie", p+"-sidecar", p+"-frame"
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM media_extras WHERE parent_id = $1`, movie)
		_, _ = pool.Exec(ctx, `DELETE FROM media_items WHERE content_id = $1`, movie)
	})
	exec(`INSERT INTO media_items (content_id,type,title) VALUES ($1,'movie','Movie')`, movie)
	exec(`INSERT INTO media_extras (content_id,parent_id,kind,title,still_path,still_source) VALUES
		($1,$3,'featurette','Sidecar','local/extras/'||$1||'/still/original.r1.webp','file:///library/movie/extras/making-of-thumb.jpg'),
		($2,$3,'featurette','Frame','local/extras/'||$2||'/still/original.r2.webp','frame:key')`, sidecarExtra, frameExtra, movie)

	next := &previewImageRecorder{}
	resolver := BloemPreviewImages(next, pool)
	preview := previewcontext.With(ctx)
	library := []string{
		"local/series/" + p + "/aa/poster/w500.r1.webp",
		"local/series/" + p + "/bb/backdrop/w1280.r1.webp",
		"local/series/" + p + "/cc/logo/w500.r1.webp",
		"local/series/" + p + "/seasons/1/episodes/1/dd/still/w300.r1.webp",
		"nfo/series/" + p + "/ee/backdrop/w1280.r1.webp",
		"local/extras/" + sidecarExtra + "/still/w300.r1.webp",
	}
	frame := "local/extras/" + frameExtra + "/still/w300.r2.webp"

	got := resolver.ResolveImageURLs(preview, append(slices.Clone(library), frame), "card")
	for _, path := range library {
		if got[path] == "" {
			t.Errorf("library artwork %s was refused", path)
		}
	}
	if got[frame] != "" {
		t.Errorf("a frame taken from the extra's video was signed: %s", frame)
	}
	if resolver.ResolveImageURL(preview, library[2], "card") == "" {
		t.Error("a library logo was refused on its own")
	}
	if resolver.ResolveImageURL(preview, frame, "card") != "" {
		t.Error("a taken frame was signed on its own")
	}
	if resolver.ResolveImageURL(ctx, frame, "card") == "" {
		t.Error("a subscriber lost the extra's frame")
	}
}
