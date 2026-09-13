package abs

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

// TestSiloItemToLibraryItemDetail_FlattensChaptersAcrossFiles verifies that
// chapters from multiple audio files use contiguous IDs and absolute offsets.
func TestSiloItemToLibraryItemDetail_FlattensChaptersAcrossFiles(t *testing.T) {
	// Given an audiobook whose chapters are distributed across two audio files.
	item := &models.MediaItem{ContentID: "book-multi"}
	files := []*models.MediaFile{
		{
			FilePath: "/x/part1.mp3",
			Duration: 120,
			Chapters: []models.MediaChapter{{Index: 0, Title: "One", StartSeconds: 0, EndSeconds: 60}},
		},
		{
			FilePath: "/x/part2.mp3",
			Duration: 90,
			Chapters: []models.MediaChapter{{Index: 0, Title: "Two", StartSeconds: 0, EndSeconds: 45}},
		},
	}

	// When the ABS item-detail response is built.
	detail := siloItemToLibraryItemDetail(item, files, AudiobookLibrary{ID: 1, Name: "Audiobooks"}, "http://x")

	// Then every file chapter is present with an absolute offset and contiguous IDs.
	if len(detail.Media.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(detail.Media.Chapters))
	}
	if detail.Media.Chapters[0].Title != "One" || detail.Media.Chapters[0].Start != 0 {
		t.Fatalf("first chapter = %+v, want title One at 0", detail.Media.Chapters[0])
	}
	if detail.Media.Chapters[1].Title != "Two" || detail.Media.Chapters[1].Start != 120 || detail.Media.Chapters[1].End != 165 {
		t.Fatalf("second chapter = %+v, want title Two at 120 ending at 165", detail.Media.Chapters[1])
	}
	if detail.Media.Chapters[0].ID != 0 || detail.Media.Chapters[1].ID != 1 {
		t.Fatalf("chapter ids = (%d, %d), want (0, 1)", detail.Media.Chapters[0].ID, detail.Media.Chapters[1].ID)
	}
}
