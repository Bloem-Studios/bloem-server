package api

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

func TestManagedAudiobookEditionRejectsAmbiguousOrIncompleteRecordings(t *testing.T) {
	part := func(id, index int) *models.MediaFile {
		return &models.MediaFile{ID: id, MediaFolderID: 7, Duration: 60, PresentationKind: "multipart", PresentationGroupKey: "recording", PresentationPartIndex: index, PresentationPartTotal: 2}
	}
	for _, tc := range []struct {
		name  string
		files []*models.MediaFile
		valid bool
	}{
		{"single", []*models.MediaFile{{ID: 1, Duration: 120}}, true},
		{"ordered parts", []*models.MediaFile{part(2, 2), part(1, 1)}, true},
		{"alternate single files", []*models.MediaFile{{ID: 1, Duration: 120}, {ID: 2, Duration: 120}}, false},
		{"missing part", []*models.MediaFile{part(1, 1)}, false},
		{"duplicate part", []*models.MediaFile{part(1, 1), part(2, 1)}, false},
		{"unknown duration", []*models.MediaFile{{ID: 1}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, duration, err := managedAudiobookEdition(tc.files)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
			if tc.valid && (duration != 120 || files[0].ID != 1) {
				t.Fatalf("wrong timeline: %v, %v", files, duration)
			}
		})
	}
	for _, field := range []string{"group", "folder", "total"} {
		t.Run(field, func(t *testing.T) {
			files := []*models.MediaFile{part(1, 1), part(2, 2)}
			switch field {
			case "group":
				files[1].PresentationGroupKey = "other"
			case "folder":
				files[1].MediaFolderID = 8
			case "total":
				files[1].PresentationPartTotal = 3
			}
			if _, _, err := managedAudiobookEdition(files); err == nil {
				t.Fatal("mixed recording accepted")
			}
		})
	}
}
