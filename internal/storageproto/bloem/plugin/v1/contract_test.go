package storagev1

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestCanonicalStorageContract(t *testing.T) {
	data, err := os.ReadFile("../../../../../proto/bloem/plugin/v1/storage_provider.proto")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != "c04eca41065b092b99d2186349bb584bf9d63ade9c23c5c653bc377fa829e872" {
		t.Fatalf("canonical schema changed: %s", got)
	}
	file := File_bloem_plugin_v1_storage_provider_proto
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
		"Source":       {"id", "name", "root_entry_id", "revision_pinned_reads"},
		"Entry":        {"id", "name", "logical_path", "kind", "size", "modified_unix_nano", "revision"},
		"ListRequest":  {"source_id", "directory_id", "cursor", "max_entries"},
		"ListResponse": {"entries", "next_cursor", "complete"},
		"StatRequest":  {"source_id", "entry_id", "expected_revision"}, "StatResponse": {"entry"},
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
