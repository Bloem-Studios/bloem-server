package storagev1

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestCanonicalStorageContract(t *testing.T) {
	data, err := os.ReadFile("../../../../../proto/bloem/plugin/v1/storage_provider.proto")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != "ee935d06d23f126ca9a11f8050264947811e81cb571f2c4ebd55143b6fb2117f" {
		t.Fatalf("canonical schema changed: %s", got)
	}
	file := File_bloem_plugin_v1_storage_provider_proto
	descriptorBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(protodesc.ToFileDescriptorProto(file))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("descriptor SHA256: %x", sha256.Sum256(descriptorBytes))
	if file.Package() != "bloem.plugin.v1" || file.Imports().Len() != 0 {
		t.Fatal("private descriptor namespace/import changed")
	}
	options, ok := file.Options().(*descriptorpb.FileOptions)
	if !ok || options.GetGoPackage() != "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/bloem/plugin/v1;storagev1" {
		t.Fatal("import mapping changed canonical descriptor options")
	}
	service := file.Services().ByName("StorageProvider")
	if service == nil || service.Methods().Len() != 4 {
		t.Fatal("service contract changed")
	}
	for _, name := range []protoreflect.Name{"Describe", "List", "Stat", "Read"} {
		method := service.Methods().ByName(name)
		if method == nil || method.IsStreamingClient() || method.IsStreamingServer() != (name == "Read") {
			t.Fatalf("method changed: %s", name)
		}
	}
	fields := map[protoreflect.Name][]protoreflect.Name{
		"DescribeRequest": {}, "DescribeResponse": {"revision", "sources"},
		"Source": {"id", "name", "root_entry_id", "revision_pinned_reads", "media_kinds", "directory_only_listing"},
		"Entry":  {"id", "name", "logical_path", "kind", "size", "modified_unix_nano", "revision", "ebook", "audiobook", "audio_file"},
		"EbookMetadata": {
			"title", "authors", "description", "publisher", "published_date", "language", "isbn",
			"series", "series_index", "genres", "page_count", "cover_entry_id", "cover_revision", "cover_thumbhash",
		},
		"AudioChapter":      {"id", "title", "start_seconds", "end_seconds"},
		"AudioFileMetadata": {"book_id", "disc_number", "track_number", "duration_seconds", "codec", "container", "bitrate", "sample_rate", "channels", "chapters"},
		"AudiobookMetadata": {"book_id", "title", "authors", "narrators", "description", "publisher", "published_date", "language", "isbn", "series", "series_index", "genres", "cover_entry_id", "cover_revision", "cover_thumbhash"},
		"ListRequest":       {"source_id", "directory_id", "cursor", "max_entries", "changes_since", "directories_only"},
		"ListResponse":      {"entries", "next_cursor", "complete", "change_token", "removed_entry_ids"},
		"StatRequest":       {"source_id", "entry_id", "expected_revision"}, "StatResponse": {"entry"},
		"ReadRequest": {"source_id", "entry_id", "expected_revision", "offset", "length"}, "ReadChunk": {"offset", "data", "eof"},
	}
	if file.Messages().Len() != len(fields) {
		t.Fatal("message contract changed")
	}
	for message, names := range fields {
		descriptor := file.Messages().ByName(message)
		if descriptor == nil || descriptor.Fields().Len() != len(names) {
			t.Fatalf("message changed: %s", message)
		}
		for i, name := range names {
			field := descriptor.Fields().ByName(name)
			if field == nil || field.Number() != protoreflect.FieldNumber(i+1) {
				t.Fatalf("field changed: %s.%s", message, name)
			}
		}
	}
	// Added fields must retain their exact scalar/message types and cardinality.
	for _, tc := range []struct {
		message, name protoreflect.Name
		kind          protoreflect.Kind
		repeated      bool
		target        protoreflect.FullName
	}{
		{"Source", "media_kinds", protoreflect.StringKind, true, ""},
		{"Source", "directory_only_listing", protoreflect.BoolKind, false, ""},
		{"ListRequest", "directories_only", protoreflect.BoolKind, false, ""},
		{"Entry", "audiobook", protoreflect.MessageKind, false, "bloem.plugin.v1.AudiobookMetadata"},
		{"Entry", "audio_file", protoreflect.MessageKind, false, "bloem.plugin.v1.AudioFileMetadata"},
		{"AudioChapter", "start_seconds", protoreflect.DoubleKind, false, ""},
		{"AudioChapter", "end_seconds", protoreflect.DoubleKind, false, ""},
		{"AudioFileMetadata", "disc_number", protoreflect.Uint32Kind, false, ""},
		{"AudioFileMetadata", "track_number", protoreflect.Uint32Kind, false, ""},
		{"AudioFileMetadata", "duration_seconds", protoreflect.DoubleKind, false, ""},
		{"AudioFileMetadata", "bitrate", protoreflect.Uint32Kind, false, ""},
		{"AudioFileMetadata", "sample_rate", protoreflect.Uint32Kind, false, ""},
		{"AudioFileMetadata", "channels", protoreflect.Uint32Kind, false, ""},
		{"AudioFileMetadata", "chapters", protoreflect.MessageKind, true, "bloem.plugin.v1.AudioChapter"},
		{"AudiobookMetadata", "authors", protoreflect.StringKind, true, ""},
		{"AudiobookMetadata", "narrators", protoreflect.StringKind, true, ""},
		{"AudiobookMetadata", "genres", protoreflect.StringKind, true, ""},
	} {
		field := file.Messages().ByName(tc.message).Fields().ByName(tc.name)
		if field.Kind() != tc.kind || field.IsList() != tc.repeated || (tc.target != "" && field.Message().FullName() != tc.target) {
			t.Fatalf("field type changed: %s.%s", tc.message, tc.name)
		}
	}
	if StorageProvider_Read_FullMethodName != "/bloem.plugin.v1.StorageProvider/Read" {
		t.Fatal("wire method changed")
	}
}

func TestPrivateDescriptorCoexistsWithPublicRuntime(t *testing.T) {
	_ = publicv1.GetManifestRequest{}
	_ = DescribeRequest{}
	for _, name := range []protoreflect.FullName{"silo.plugin.v1.Runtime", "bloem.plugin.v1.StorageProvider"} {
		if _, err := protoregistry.GlobalFiles.FindDescriptorByName(name); err != nil {
			t.Fatal(err)
		}
	}
}
