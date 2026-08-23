package catalogseed

import (
	"bytes"
	"compress/gzip"
	"errors"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

func compressedCatalogSeedPayload(t *testing.T, payload []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestDecodeBundleWithinLimitsRejectsExpandedOversize(t *testing.T) {
	data := compressedCatalogSeedPayload(t, bytes.Repeat([]byte(" "), 65))
	if _, err := decodeBundleWithinLimits(data, int64(len(data)), 64); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("decode error = %v, want ErrInvalidBundle", err)
	}
}

func TestDecodeBundleWithinLimitsRejectsAdditionalGzipMemberAndTrailingBytes(t *testing.T) {
	member := compressedCatalogSeedPayload(t, []byte(`{}`))
	for name, data := range map[string][]byte{
		"additional member": append(append([]byte(nil), member...), member...),
		"trailing bytes":    append(append([]byte(nil), member...), []byte("trailing")...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeBundleWithinLimits(data, int64(len(data)), 1024); !errors.Is(err, ErrInvalidBundle) {
				t.Fatalf("decode error = %v, want ErrInvalidBundle", err)
			}
		})
	}
}

func TestToVideoTrackRecordsPreservesVideoMetadata(t *testing.T) {
	got := toVideoTrackRecords([]models.VideoTrack{
		{ColorRange: "tv", DVLevel: 6},
		{ColorRange: "pc"},
		{ColorRange: "unknown"},
	})

	if len(got) != 3 {
		t.Fatalf("records length = %d, want 3", len(got))
	}
	if got[0].ColorRange != "tv" || got[1].ColorRange != "pc" || got[2].ColorRange != "unknown" {
		t.Fatalf(
			"ColorRange values = [%q, %q, %q], want [tv, pc, unknown]",
			got[0].ColorRange,
			got[1].ColorRange,
			got[2].ColorRange,
		)
	}
	if got[0].DVLevel != 6 {
		t.Fatalf("DVLevel = %d, want 6", got[0].DVLevel)
	}
}

func TestCatalogSeedSearchUpsertIDsIncludesChangedItemsAndEmbeddings(t *testing.T) {
	itemStates := map[string]bool{
		"movie-1":  true,
		"movie-2":  false,
		"series-1": true,
	}
	embeddings := []EmbeddingRecord{
		{MediaItemID: " movie-3 "},
		{MediaItemID: "movie-1"},
		{MediaItemID: ""},
	}
	files := []FileRecord{
		{ContentID: " movie-4 "},
		{ContentID: ""},
	}
	links := []LibraryLinkRecord{
		{ContentID: "movie-5"},
		{ContentID: "movie-2"},
	}

	got := catalogSeedSearchUpsertIDs(itemStates, embeddings, files, links)
	want := []string{"movie-1", "movie-2", "movie-3", "movie-4", "movie-5", "series-1"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalogSeedSearchUpsertIDs = %#v, want %#v", got, want)
	}
}
