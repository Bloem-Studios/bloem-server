package catalog

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanbatch"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

func nativeShapeInput(t *testing.T) NativePublicationInput {
	t.Helper()
	binding := uuid.New()
	entry := &storagev1.Entry{Id: "main", Revision: "1", LogicalPath: "Book.epub", Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 123, ModifiedUnixNano: time.Date(2026, 1, 1, 1, 1, 1, 123456789, time.UTC).UnixNano()}
	location, err := storagesource.CatalogLocation(binding, entry.Id)
	if err != nil {
		t.Fatal(err)
	}
	modified := models.NormalizeFileModifiedAt(time.Unix(0, entry.ModifiedUnixNano))
	return NativePublicationInput{FolderID: 1, ItemKey: "prepared-item", Claim: storagesource.IngestionClaim{Lease: storagesource.IngestionLease{BindingID: binding}, Entry: entry},
		File: models.MediaFile{ContentID: "prepared-item", MediaFolderID: 1, FilePath: location, CanonicalRootPath: location, ObservedRootPath: location,
			ContentGroupKey: "bloem-native:" + binding.String() + ":ebook:isbn:123", GroupKeyVersion: 1, BaseTitle: "Book", BaseType: "ebook",
			FileSize: entry.Size, FileModifiedAt: &modified, Container: "epub", ProbeSource: "native"}}
}
func TestNativeOnboardingPublicationShape(t *testing.T) {
	input := nativeShapeInput(t)
	data, err := nativePublicationShape(scanbatch.WithRunID(context.Background(), "queue-run"), input)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]any
	if err = json.Unmarshal(data, &shape); err != nil {
		t.Fatal(err)
	}
	if shape["first_seen_scan_run_id"] != "queue-run" {
		t.Fatal("shape lost actual scan queue provenance from repository context")
	}
	if shape["container"] != "epub" || shape["canonical_root_path"] != input.File.FilePath || shape["observed_root_path"] != input.File.FilePath {
		t.Fatal("opaque root/container shape changed")
	}
}
func TestNativeOnboardingPublicationShapeNegative(t *testing.T) {
	for _, change := range []struct {
		name  string
		apply func(*NativePublicationInput)
	}{
		{"local-probe", func(p *NativePublicationInput) { p.File.ProbeSource = "local" }},
		{"root", func(p *NativePublicationInput) { p.File.CanonicalRootPath = "" }},
		{"other-root", func(p *NativePublicationInput) { p.File.ObservedRootPath = "other" }},
		{"unsupported-container", func(p *NativePublicationInput) { p.File.Container = "mkv" }},
		{"size", func(p *NativePublicationInput) { p.File.FileSize++ }},
		{"un-normalized-mtime", func(p *NativePublicationInput) {
			v := time.Unix(0, p.Claim.Entry.ModifiedUnixNano)
			p.File.FileModifiedAt = &v
		}},
		{"extra", func(p *NativePublicationInput) { p.File.ExtraID = "extra" }},
		{"episode", func(p *NativePublicationInput) { p.File.EpisodeID = "episode" }},
		{"video", func(p *NativePublicationInput) { p.File.CodecVideo = "h264" }},
		{"wrong-group", func(p *NativePublicationInput) { p.File.ContentGroupKey = "ebook:ordinary" }},
		{"wrong-item", func(p *NativePublicationInput) { p.File.ContentID = "other" }},
		{"missing", func(p *NativePublicationInput) { v := time.Now(); p.File.MissingSince = &v }},
	} {
		t.Run(change.name, func(t *testing.T) {
			input := nativeShapeInput(t)
			change.apply(&input)
			if _, err := nativePublicationShape(context.Background(), input); err == nil {
				t.Fatal("invalid preparation accepted")
			}
		})
	}
}
