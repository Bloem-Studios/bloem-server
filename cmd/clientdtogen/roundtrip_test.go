package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
	clientv1 "github.com/Silo-Server/silo-server/contracts/client/v1"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// The round-trip gate of the generator test strategy
// (docs/specs/client-dto-generator.md §8): for every playback root, the Go
// wire type must accept the server's own golden body and re-emit it, and every
// key in that body — and in the marshaled form of the realtime roots, which
// have no fixture file — must appear as an @SerialName in the committed
// generated Kotlin. This proves key coverage from the server side, against
// what clients actually vendor, without a Kotlin compiler in Go CI.

const (
	playbackPkgPath  = "internal/playback"
	playbackFixtures = "internal/playback/testdata/protocol_v3"
	committedKotlin  = "contracts/client/v1/kotlin/playback/Playback.kt"
)

// rootCases maps every registered playback root to its wire body source: the
// golden fixture file when one exists, otherwise a fully populated sample
// value whose marshal is the body. The registry lockstep check below fails
// when a root is added or renamed without updating this table.
var rootCases = []struct {
	goType  string
	fixture string
	target  func() any
	sample  any
}{
	{"StartRequestV3", "start_request.json", func() any { return &playback.StartRequestV3{} }, nil},
	{"ReplanRequestV3", "replan_request.json", func() any { return &playback.ReplanRequestV3{} }, nil},
	{"RouteEventV3", "route_event.json", func() any { return &playback.RouteEventV3{} }, nil},
	{"DecisionResponseV3", "decision_response.json", func() any { return &playback.DecisionResponseV3{} }, nil},
	{"CapabilityResponseV3", "capability_response.json", func() any { return &playback.CapabilityResponseV3{} }, nil},
	{"ErrorResponseV3", "error_response.json", func() any { return &playback.ErrorResponseV3{} }, nil},
	{"EventEnvelope", "", func() any { return &playback.EventEnvelope{} }, sampleEventEnvelope()},
	{"CommandEnvelope", "", func() any { return &playback.CommandEnvelope{} }, sampleCommandEnvelope()},
	{"HelloEnvelope", "", func() any { return &playback.HelloEnvelope{} }, sampleHelloEnvelope()},
	{"AckEnvelope", "", func() any { return &playback.AckEnvelope{} }, sampleAckEnvelope()},
	{"ResultEnvelope", "", func() any { return &playback.ResultEnvelope{} }, sampleResultEnvelope()},
	{"ChapterThumbnailReadyPayload", "", func() any { return &playback.ChapterThumbnailReadyPayload{} }, sampleChapterThumbnailReady()},
}

// The realtime samples set every field, omitempty included, so the walk below
// sees each root's whole key set rather than only the always-present keys.
func sampleEventEnvelope() playback.EventEnvelope {
	return playback.EventEnvelope{
		Type:      playback.RealtimeMessageTypeEvent,
		SessionID: "session-roundtrip",
		Name:      playback.RealtimeEventChapterThumbnailReady,
		Payload:   json.RawMessage(`{"session_id":"session-roundtrip","file_id":42,"chapter_index":1,"thumbnail_url":"/thumb"}`),
	}
}

func sampleCommandEnvelope() playback.CommandEnvelope {
	return playback.CommandEnvelope{
		Type:       playback.RealtimeMessageTypeCommand,
		CommandID:  "cmd-roundtrip",
		SessionID:  "session-roundtrip",
		Name:       playback.CommandPause,
		Reason:     "roundtrip",
		IssuedBy:   &playback.CommandIssuedBy{Kind: "server"},
		DeadlineMS: 500,
		Payload:    json.RawMessage(`{"position_ms":1000}`),
	}
}

func sampleHelloEnvelope() playback.HelloEnvelope {
	return playback.HelloEnvelope{
		Type:         playback.RealtimeMessageTypeHello,
		SessionID:    "session-roundtrip",
		Client:       playback.HelloClientInfo{Name: "roundtrip", Version: "1"},
		Capabilities: playback.HelloCapabilities{Commands: []playback.CommandName{playback.CommandPause, playback.CommandSeek}},
	}
}

func sampleAckEnvelope() playback.AckEnvelope {
	return playback.AckEnvelope{
		Type:      playback.RealtimeMessageTypeAck,
		CommandID: "cmd-roundtrip",
		SessionID: "session-roundtrip",
		Status:    playback.RealtimeAckStatusAccepted,
	}
}

func sampleResultEnvelope() playback.ResultEnvelope {
	return playback.ResultEnvelope{
		Type:      playback.RealtimeMessageTypeResult,
		CommandID: "cmd-roundtrip",
		SessionID: "session-roundtrip",
		Status:    playback.RealtimeResultStatusRejected,
		Error:     "not_supported",
	}
}

func sampleChapterThumbnailReady() playback.ChapterThumbnailReadyPayload {
	return playback.ChapterThumbnailReadyPayload{
		SessionID:          "session-roundtrip",
		FileID:             42,
		ChapterIndex:       1,
		ThumbnailURL:       "/thumb",
		ThumbnailThumbhash: "0AwR1UYUHIA",
	}
}

func TestPlaybackRootRoundTrip(t *testing.T) {
	root, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	rawReg, err := clientv1.FS.ReadFile("registry.json")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Parse(rawReg)
	if err != nil {
		t.Fatal(err)
	}
	checkRegistryLockstep(t, reg)

	g, err := graph.Build(graph.Config{Dir: root, Registry: reg})
	if err != nil {
		t.Fatalf("building type graph: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, committedKotlin))
	if err != nil {
		t.Fatalf("reading committed Kotlin (%s): %v; run make client-dtos", committedKotlin, err)
	}
	serials := parseSerialNames(string(content))

	for _, tc := range rootCases {
		t.Run(tc.goType, func(t *testing.T) {
			key := playbackPkgPath + "." + tc.goType
			typ, ok := g.Type(key)
			if !ok {
				t.Fatalf("%s not in the graph", key)
			}
			if !typ.Root {
				t.Errorf("%s is not a graph root; registry and graph disagree", key)
			}
			if _, ok := serials[tc.goType]; !ok {
				t.Fatalf("%s has no class in %s; run make client-dtos", tc.goType, committedKotlin)
			}

			var bodies [][2]string // label, wire JSON
			if tc.fixture != "" {
				fixturePath := filepath.Join(root, playbackFixtures, tc.fixture)
				fixture, err := os.ReadFile(fixturePath)
				if err != nil {
					t.Fatal(err)
				}
				target := tc.target()
				if err := json.Unmarshal(fixture, target); err != nil {
					t.Fatalf("Go type no longer accepts its golden fixture %s: %v", tc.fixture, err)
				}
				remarshaled, err := json.Marshal(target)
				if err != nil {
					t.Fatalf("re-marshaling %s: %v", tc.fixture, err)
				}
				assertSameJSON(t, tc.fixture, fixture, remarshaled)
				bodies = [][2]string{{"fixture " + tc.fixture, string(fixture)}, {"re-marshaled " + tc.fixture, string(remarshaled)}}
			} else {
				marshaled, err := json.Marshal(tc.sample)
				if err != nil {
					t.Fatalf("marshaling sample %s: %v", tc.goType, err)
				}
				back := tc.target()
				if err := json.Unmarshal(marshaled, back); err != nil {
					t.Fatalf("sample %s does not survive its own round-trip: %v", tc.goType, err)
				}
				bodies = [][2]string{{"sample " + tc.goType, string(marshaled)}}
			}

			for _, body := range bodies {
				var value any
				if err := json.Unmarshal([]byte(body[1]), &value); err != nil {
					t.Fatalf("parsing %s: %v", body[0], err)
				}
				walkWire(t, g, serials, body[0], value, graph.TypeRef{Kind: graph.KindStruct, Named: key})
			}
		})
	}
}

// checkRegistryLockstep fails when the registry's playback roots and the
// table above drift apart in either direction.
func checkRegistryLockstep(t *testing.T, reg *registry.Registry) {
	t.Helper()
	registered := map[string]bool{}
	for _, pkg := range reg.Packages {
		if pkg.Path != playbackPkgPath {
			continue
		}
		for _, root := range pkg.Roots {
			registered[root.Type] = true
		}
	}
	if len(registered) == 0 {
		t.Fatalf("no roots registered for %s in the registry", playbackPkgPath)
	}
	tabled := map[string]bool{}
	for _, tc := range rootCases {
		tabled[tc.goType] = true
		if (tc.fixture != "") == (tc.sample != nil) {
			t.Errorf("%s must have exactly one wire body source: fixture file or sample value", tc.goType)
		}
	}
	for name := range registered {
		if !tabled[name] {
			t.Errorf("registry root %s has no round-trip case; add one to rootCases", name)
		}
	}
	for name := range tabled {
		if !registered[name] {
			t.Errorf("round-trip case %s is not a registered root; remove it from rootCases", name)
		}
	}
}

// assertSameJSON fails when the Go type re-marshals a golden fixture into a
// different JSON document — the fixture and the live wire shape have drifted,
// which make playback-fixtures owns resolving.
func assertSameJSON(t *testing.T, label string, want, got []byte) {
	t.Helper()
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("parsing %s: %v", label, err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("parsing re-marshaled %s: %v", label, err)
	}
	if !reflect.DeepEqual(w, g) {
		t.Errorf("%s: the Go type round-trips the fixture into a different document; run make playback-fixtures and re-review", label)
	}
}

var (
	kotlinClassDecl = regexp.MustCompile(`^public (?:data class|class|value class) (\w+)`)
	// Struct properties sit at exactly four spaces of indent; enum constants
	// live in the companion object at eight, so the anchor tells them apart.
	kotlinSerialName = regexp.MustCompile(`^    @SerialName\("([^"]*)"\)$`)
)

// parseSerialNames reads the committed Kotlin into class → wire-name set for
// the struct classes, the @SerialName lines the coverage check is about.
func parseSerialNames(content string) map[string]map[string]bool {
	classes := map[string]map[string]bool{}
	current := ""
	for _, line := range strings.Split(content, "\n") {
		switch line {
		case ")", "}":
			current = ""
			continue
		}
		if m := kotlinClassDecl.FindStringSubmatch(line); m != nil {
			current = m[1]
			if classes[current] == nil {
				classes[current] = map[string]bool{}
			}
			continue
		}
		if current == "" {
			continue
		}
		if m := kotlinSerialName.FindStringSubmatch(line); m != nil {
			classes[current][m[1]] = true
		}
	}
	return classes
}

// walkWire descends a decoded wire body alongside the type graph. At every
// struct-shaped object it requires each key to be a field of the graph type
// and an @SerialName of the committed Kotlin class, then recurses into the
// field's type. Map keys are data, not contract names; raw payloads, enums
// and scalars are leaves.
func walkWire(t *testing.T, g *graph.Graph, serials map[string]map[string]bool, path string, v any, ref graph.TypeRef) {
	t.Helper()
	if v == nil {
		return
	}
	switch ref.Kind {
	case graph.KindStruct:
		obj, ok := v.(map[string]any)
		if !ok {
			t.Errorf("%s: want an object for %s, got %T", path, ref.Named, v)
			return
		}
		typ, ok := g.Type(ref.Named)
		if !ok {
			t.Fatalf("%s: graph type %s disappeared", path, ref.Named)
		}
		for _, key := range sortedKeys(obj) {
			keyPath := path + "." + key
			var field *graph.Field
			for i := range typ.Fields {
				if typ.Fields[i].WireName == key {
					field = &typ.Fields[i]
					break
				}
			}
			if field == nil {
				t.Errorf("%s: wire key is not a field of %s", keyPath, ref.Named)
				continue
			}
			if !serials[typ.Name][key] {
				t.Errorf("%s: no @SerialName in committed Kotlin class %s", keyPath, typ.Name)
			}
			walkWire(t, g, serials, keyPath, obj[key], field.Type)
		}
	case graph.KindMap:
		obj, ok := v.(map[string]any)
		if !ok {
			t.Errorf("%s: want an object for map, got %T", path, v)
			return
		}
		for _, key := range sortedKeys(obj) {
			walkWire(t, g, serials, path+"."+key, obj[key], *ref.Elem)
		}
	case graph.KindList:
		arr, ok := v.([]any)
		if !ok {
			t.Errorf("%s: want an array, got %T", path, v)
			return
		}
		for i, elem := range arr {
			walkWire(t, g, serials, fmt.Sprintf("%s[%d]", path, i), elem, *ref.Elem)
		}
	}
}

// sortedKeys exists only for stable failure messages.
func sortedKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
