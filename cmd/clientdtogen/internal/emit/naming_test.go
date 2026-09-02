package emit

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
)

var kotlinKeywords = map[string]bool{"in": true, "is": true, "as": true, "fun": true, "object": true}

// TestCamelCase pins docs/specs/client-dto-generator.md §4.3, including every
// initialism example and the keyword case.
func TestCamelCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"HDRDetails", "hdrDetails"},
		{"BLCompatibilityIDs", "blCompatibilityIds"},
		{"HDR10MaxWidth", "hdr10MaxWidth"},
		{"DVProfile", "dvProfile"},
		{"MIMEType", "mimeType"},
		{"ID", "id"},
		{"URL", "url"},
		{"InApp", "inApp"},
		{"Type", "type"},
		{"In", "inValue"},
		{"Is", "isValue"},
		{"As", "asValue"},
		{"Fun", "funValue"},
		{"Object", "objectValue"},
		{"SourceStartSeconds", "sourceStartSeconds"},
		{"ThumbnailURL", "thumbnailUrl"},
		{"OSVersion", "osVersion"},
		{"ASSStyling", "assStyling"},
		{"DeadlineMS", "deadlineMs"},
		{"StreamHLSV3", "streamHlsV3"},
		{"Codec2Pass", "codec2Pass"},
		{"Snake_case_name", "snakeCaseName"},
		{"lowerStart", "lowerStart"},
		{"X", "x"},
	}
	for _, tc := range cases {
		if got := CamelCase(tc.in, kotlinKeywords); got != tc.want {
			t.Errorf("CamelCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"HDRDetails", []string{"HDR", "Details"}},
		{"BLCompatibilityIDs", []string{"BL", "Compatibility", "IDs"}},
		{"HDR10MaxWidth", []string{"HDR", "10", "Max", "Width"}},
		{"StreamHTTPProgressiveV3", []string{"Stream", "HTTP", "Progressive", "V", "3"}},
		{"StreamHLSV3", []string{"Stream", "HLS", "V", "3"}},
		{"UUID", []string{"UUID"}},
		{"a_b", []string{"a", "b"}},
	}
	for _, tc := range cases {
		if got := Words(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Words(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func enumType(name string, consts ...string) *graph.Type {
	typ := &graph.Type{Name: name, Kind: graph.KindEnum, Package: &graph.Package{Path: "internal/x"}}
	for _, c := range consts {
		typ.Constants = append(typ.Constants, graph.Constant{GoName: c, Value: strings.ToLower(c)})
	}
	return typ
}

// TestConstantNames pins §4.2: type-name prefix, shared word prefix/suffix,
// single constants, digits, and the collision refusal.
func TestConstantNames(t *testing.T) {
	cases := []struct {
		typ  *graph.Type
		want []string
	}{
		{enumType("StreamProtocolV3", "StreamProtocolHLSV3", "StreamProtocolProgressiveV3"), []string{"HLS", "PROGRESSIVE"}},
		{enumType("StreamProtocolV3", "StreamHTTPProgressiveV3", "StreamHLSV3"), []string{"HTTP_PROGRESSIVE", "HLS"}},
		{enumType("Protocol", "ProtocolHLS", "ProtocolProgressive", "ProtocolLate"), []string{"HLS", "PROGRESSIVE", "LATE"}},
		{enumType("RealtimeAckStatus", "RealtimeAckStatusAccepted"), []string{"ACCEPTED"}},
		{enumType("CommandName", "CommandPause", "CommandPlayPause"), []string{"PAUSE", "PLAY_PAUSE"}},
		{enumType("EnhancementLayerV3", "EnhancementNoneV3", "EnhancementMELV3"), []string{"NONE", "MEL"}},
		{enumType("Level", "LevelHDR10", "LevelHDR10Plus"), []string{"HDR10", "HDR10_PLUS"}},
		{enumType("Codec", "Codec2Pass", "CodecCRF"), []string{"_2_PASS", "CRF"}},
		{enumType("Same", "Same", "SameOther"), []string{"SAME", "SAME_OTHER"}},
		{enumType("Empty"), nil},
	}
	for _, tc := range cases {
		got, err := ConstantNames(tc.typ)
		if err != nil {
			t.Errorf("%s: %v", tc.typ.Name, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.typ.Name, got, tc.want)
		}
	}
	_, err := ConstantNames(enumType("Dup", "DupHLS", "DupHls"))
	if err == nil || !strings.Contains(err.Error(), "internal/x.Dup") || !strings.Contains(err.Error(), "HLS") {
		t.Errorf("collision error = %v, want the type and identifier named", err)
	}
}

func TestFilesWriteRefusesEscape(t *testing.T) {
	dir := t.TempDir()
	if err := (Files{{Path: "../x.kt"}}).Write(dir); err == nil {
		t.Error("Write accepted a path outside the root")
	}
	if err := (Files{{Path: "a/B.kt", Content: []byte("x")}}).Write(dir); err != nil {
		t.Fatal(err)
	}
}
