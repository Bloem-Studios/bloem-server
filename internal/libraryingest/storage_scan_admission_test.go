package libraryingest

import (
	"strings"
	"testing"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func TestScanDescribeAdmissionRejectsAmbiguousIdentity(t *testing.T) {
	source := storagesource.SourceConfig{ProviderSourceID: "books", RootEntryID: "root"}
	for _, kind := range []string{"duplicate-other", "nil-source", "empty-source", "empty-root", "nul-source", "nul-root", "invalid-utf8", "long-source", "long-root"} {
		t.Run(kind, func(t *testing.T) {
			d := &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: "books", RootEntryId: "root", RevisionPinnedReads: true}, {Id: "other", RootEntryId: "root", RevisionPinnedReads: true}}}
			switch kind {
			case "duplicate-other":
				d.Sources = append(d.Sources, &storagev1.Source{Id: "other", RootEntryId: "different"})
			case "nil-source":
				d.Sources = append(d.Sources, nil)
			case "empty-source":
				d.Sources[1].Id = ""
			case "empty-root":
				d.Sources[1].RootEntryId = ""
			case "nul-source":
				d.Sources[1].Id = "bad\x00id"
			case "nul-root":
				d.Sources[1].RootEntryId = "bad\x00root"
			case "invalid-utf8":
				d.Sources[1].Id = string([]byte{0xff})
			case "long-source":
				d.Sources[1].Id = strings.Repeat("s", 1025)
			case "long-root":
				d.Sources[1].RootEntryId = strings.Repeat("r", 1025)
			}
			if err := describedSource(d, source); err == nil {
				t.Fatal("scan admitted a malformed or ambiguous provider description")
			}
		})
	}
}
