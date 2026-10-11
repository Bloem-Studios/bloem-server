package storagev1

import (
	"bytes"
	"encoding/hex"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestLegacyEbookEntriesKeepWireMeaning(t *testing.T) {
	for _, tc := range []struct{ name, fixture string }{
		{"b.epub", "0a01651206622e657075621a06622e657075622001280c30073a0176420d0a04426f6f6b1205416c696365"},
		{"b.pdf", "0a01651205622e7064661a05622e7064662001280c30073a0176420d0a04426f6f6b1205416c696365"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := hex.DecodeString(tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			var entry Entry
			if err := proto.Unmarshal(wire, &entry); err != nil {
				t.Fatal(err)
			}
			if entry.GetId() != "e" || entry.GetName() != tc.name || entry.GetLogicalPath() != tc.name || entry.GetKind() != EntryKind_ENTRY_KIND_FILE || entry.GetSize() != 12 || entry.GetModifiedUnixNano() != 7 || entry.GetRevision() != "v" || entry.GetEbook().GetTitle() != "Book" || len(entry.GetEbook().GetAuthors()) != 1 || entry.GetEbook().GetAuthors()[0] != "Alice" {
				t.Fatalf("legacy entry changed: %v", &entry)
			}
			encoded, err := proto.Marshal(&entry)
			if err != nil || !bytes.Equal(encoded, wire) {
				t.Fatalf("legacy entry wire changed: %x, %v", encoded, err)
			}
			// Known audio extensions also leave the existing ebook meaning intact.
			audioFields, err := hex.DecodeString("4a060a016212014152030a0162")
			if err != nil {
				t.Fatal(err)
			}
			if err := proto.Unmarshal(append(append([]byte(nil), wire...), audioFields...), &entry); err != nil {
				t.Fatal(err)
			}
			if entry.GetEbook().GetTitle() != "Book" || entry.GetEbook().GetAuthors()[0] != "Alice" || entry.GetAudiobook().GetTitle() != "A" || entry.GetAudioFile().GetBookId() != "b" {
				t.Fatalf("audio changed ebook meaning: %v", &entry)
			}
			// An unknown future field survives without changing ebook interpretation.
			future := append(append([]byte(nil), wire...), 0xa0, 0x06, 0x01)
			if err := proto.Unmarshal(future, &entry); err != nil {
				t.Fatal(err)
			}
			encoded, err = proto.Marshal(&entry)
			if err != nil || !bytes.Equal(encoded, future) || entry.GetEbook().GetTitle() != "Book" {
				t.Fatalf("unknown field changed ebook: %x, %v", encoded, err)
			}
		})
	}
}

func TestExistingStorageRequestsKeepWireTags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message proto.Message
		fixture string
	}{
		{"Describe", &DescribeRequest{}, ""},
		{"List", &ListRequest{SourceId: "s", DirectoryId: "d", Cursor: "c", MaxEntries: 2, ChangesSince: "t"}, "0a01731201641a016320022a0174"},
		{"Stat", &StatRequest{SourceId: "s", EntryId: "e", ExpectedRevision: "v"}, "0a01731201651a0176"},
		{"Read", &ReadRequest{SourceId: "s", EntryId: "e", ExpectedRevision: "v", Offset: 2, Length: 3}, "0a01731201651a017620022803"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := proto.Marshal(tc.message)
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(wire) != tc.fixture {
				t.Fatalf("request tags changed: %x", wire)
			}
		})
	}
}

func TestAdditiveAudioAndBrowsingFieldsDecode(t *testing.T) {
	for _, tc := range []struct {
		message, fixture string
		fields           map[protoreflect.Name]protoreflect.FieldNumber
	}{
		{"Source", "0a01732a0565626f6f6b2a09617564696f626f6f6b3001", map[protoreflect.Name]protoreflect.FieldNumber{"media_kinds": 5, "directory_only_listing": 6}},
		{"ListRequest", "0a01731201643001", map[protoreflect.Name]protoreflect.FieldNumber{"directories_only": 6}},
		{"Entry", "0a01654a060a016212014152030a0162", map[protoreflect.Name]protoreflect.FieldNumber{"audiobook": 9, "audio_file": 10}},
	} {
		t.Run(tc.message, func(t *testing.T) {
			descriptor := File_bloem_plugin_v1_storage_provider_proto.Messages().ByName(protoreflect.Name(tc.message))
			for name, number := range tc.fields {
				field := descriptor.Fields().ByName(name)
				if field == nil || field.Number() != number {
					t.Fatalf("missing additive field %s.%s=%d", tc.message, name, number)
				}
			}
			wire, err := hex.DecodeString(tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			message := dynamicpb.NewMessage(descriptor)
			if err := proto.Unmarshal(wire, message); err != nil {
				t.Fatal(err)
			}
			for name := range tc.fields {
				if !message.Has(descriptor.Fields().ByName(name)) {
					t.Fatalf("field %s not decoded", name)
				}
			}
			encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
			if err != nil || !bytes.Equal(encoded, wire) {
				t.Fatalf("additive field wire changed: %x, %v", encoded, err)
			}
		})
	}
}

func TestLegacySourceDoesNotDeclareAudioOrDirectoryOnlyReadiness(t *testing.T) {
	descriptor := (&Source{}).ProtoReflect().Descriptor()
	kinds := descriptor.Fields().ByName("media_kinds")
	directories := descriptor.Fields().ByName("directory_only_listing")
	if kinds == nil || directories == nil {
		t.Fatal("missing explicit readiness declarations")
	}
	var source Source
	if err := proto.Unmarshal([]byte{0x0a, 1, 's', 0x20, 1}, &source); err != nil {
		t.Fatal(err)
	}
	if source.ProtoReflect().Get(kinds).List().Len() != 0 || source.ProtoReflect().Get(directories).Bool() {
		t.Fatal("legacy source acquired audio readiness")
	}
}
