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
// generated Kotlin and as a CodingKeys case in the committed generated Swift.
// This proves key coverage from the server side, against what both clients
// actually vendor, without a Kotlin or Swift compiler in Go CI.

const (
	playbackPkgPath  = "internal/playback"
	playbackFixtures = "internal/playback/testdata/protocol_v3"
	committedKotlin  = "contracts/client/v1/kotlin/playback/Playback.kt"
	committedSwift   = "contracts/client/v1/swift/playback/Playback.swift"
)

// target is one client's committed generated file: its language, its path and
// the class → wire-name-set map parsed out of it. Both are checked over the
// same wire bodies so neither client can silently lose a key.
type target struct {
	lang   string
	path   string
	serial map[string]map[string]bool
}

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
	{"MarkersUpdatedPayload", "", func() any { return &playback.MarkersUpdatedPayload{} }, sampleMarkersUpdated()},
	{"SubtitleReadyPayload", "", func() any { return &playback.SubtitleReadyPayload{} }, sampleSubtitleReady()},
	{"SubtitleTranslationStartedPayload", "", func() any { return &playback.SubtitleTranslationStartedPayload{} }, sampleSubtitleTranslationStarted()},
	{"SubtitleTranslationCuesPayload", "", func() any { return &playback.SubtitleTranslationCuesPayload{} }, sampleSubtitleTranslationCues()},
	{"SubtitleTranslationCompletedPayload", "", func() any { return &playback.SubtitleTranslationCompletedPayload{} }, sampleSubtitleTranslationCompleted()},
	{"SubtitleTranslationFailedPayload", "", func() any { return &playback.SubtitleTranslationFailedPayload{} }, sampleSubtitleTranslationFailed()},
	{"PlanInvalidatedPayload", "", func() any { return &playback.PlanInvalidatedPayload{} }, samplePlanInvalidated()},
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

func sampleMarkersUpdated() playback.MarkersUpdatedPayload {
	return playback.MarkersUpdatedPayload{
		SessionID: "session-roundtrip",
		FileID:    42,
		Intro:     &playback.TimeRangePayload{Start: 10.5, End: 75.25},
		Credits:   &playback.TimeRangePayload{Start: 1200, End: 1320.75},
		Recap:     &playback.TimeRangePayload{Start: 0, End: 42.5},
		Preview:   &playback.TimeRangePayload{Start: 1380, End: 1420},
	}
}

// sampleSubtitleInventoryItem sets every field of the track reference the
// subtitle payloads carry, omitempty included, so the walk sees its whole key
// set.
func sampleSubtitleInventoryItem() playback.SubtitleInventoryItemV3 {
	return playback.SubtitleInventoryItemV3{
		TrackID:         "file:42:subtitle:3",
		CombinedIndex:   3,
		Source:          playback.SubtitleSourceDownloadedV3,
		Codec:           "subrip",
		Language:        "de",
		Label:           "Deutsch (translated)",
		Forced:          false,
		Default:         true,
		HearingImpaired: true,
		Delivery:        playback.SubtitleDeliverySidecarV3,
		URL:             "/stream/session-roundtrip/subtitles/3.vtt?file_id=42",
		FontBundleURL:   "/stream/session-roundtrip/subtitles/3/fonts?file_id=42",
	}
}

func sampleSubtitleReady() playback.SubtitleReadyPayload {
	track := sampleSubtitleInventoryItem()
	return playback.SubtitleReadyPayload{
		SessionID:  "session-roundtrip",
		FileID:     42,
		SubtitleID: 7,
		Language:   "de",
		Label:      "Deutsch (translated)",
		Track:      &track,
	}
}

func sampleSubtitleTranslationStarted() playback.SubtitleTranslationStartedPayload {
	return playback.SubtitleTranslationStartedPayload{
		SessionID: "session-roundtrip",
		FileID:    42,
		JobID:     9001,
		TrackKey:  "file:42:subtitle:live:9001",
		Language:  "de",
		Label:     "Deutsch (live)",
		TotalCues: 812,
	}
}

func sampleSubtitleTranslationCues() playback.SubtitleTranslationCuesPayload {
	return playback.SubtitleTranslationCuesPayload{
		SessionID: "session-roundtrip",
		FileID:    42,
		JobID:     9001,
		TrackKey:  "file:42:subtitle:live:9001",
		Cues: []playback.StreamCue{
			{Start: 61.25, End: 64.5, Text: "Erste Zeile\nzweite Zeile"},
			{Start: 65, End: 68.75, Text: "Dritte Zeile"},
		},
		Done:  120,
		Total: 812,
	}
}

func sampleSubtitleTranslationCompleted() playback.SubtitleTranslationCompletedPayload {
	track := sampleSubtitleInventoryItem()
	return playback.SubtitleTranslationCompletedPayload{
		SessionID:  "session-roundtrip",
		FileID:     42,
		JobID:      9001,
		TrackKey:   "file:42:subtitle:live:9001",
		SubtitleID: 7,
		Language:   "de",
		Label:      "Deutsch (translated)",
		Track:      &track,
	}
}

func sampleSubtitleTranslationFailed() playback.SubtitleTranslationFailedPayload {
	return playback.SubtitleTranslationFailedPayload{
		SessionID: "session-roundtrip",
		FileID:    42,
		JobID:     9001,
		TrackKey:  "file:42:subtitle:live:9001",
		Message:   "translation provider unavailable",
	}
}

func samplePlanInvalidated() playback.PlanInvalidatedPayload {
	return playback.PlanInvalidatedPayload{
		Reason: playback.PlanInvalidatedVideoCopyUnsafe,
		PlanID: "plan-roundtrip",
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
	targets := []target{
		{lang: "Kotlin", path: committedKotlin, serial: readCommitted(t, root, committedKotlin, parseSerialNames)},
		{lang: "Swift", path: committedSwift, serial: readCommitted(t, root, committedSwift, parseCodingKeys)},
	}

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
			for _, tgt := range targets {
				if _, ok := tgt.serial[tc.goType]; !ok {
					t.Fatalf("%s has no %s type in %s; run make client-dtos", tc.goType, tgt.lang, tgt.path)
				}
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
				walkWire(t, g, targets, body[0], value, graph.TypeRef{Kind: graph.KindStruct, Named: key})
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

// swiftStructDecl and swiftCodingKey anchor on indentation the same way: a
// wire struct is nested one level inside its namespace enum, and only a
// CodingKeys case sits three levels in.
var (
	swiftStructDecl = regexp.MustCompile("^    public struct `?(\\w+)`?:")
	swiftCodingKey  = regexp.MustCompile(`^            case \w+ = "([^"]*)"$`)
)

// parseCodingKeys reads the committed Swift into type → wire-name set.
func parseCodingKeys(content string) map[string]map[string]bool {
	types := map[string]map[string]bool{}
	current := ""
	for _, line := range strings.Split(content, "\n") {
		if m := swiftStructDecl.FindStringSubmatch(line); m != nil {
			current = m[1]
			if types[current] == nil {
				types[current] = map[string]bool{}
			}
			continue
		}
		if current == "" {
			continue
		}
		if m := swiftCodingKey.FindStringSubmatch(line); m != nil {
			types[current][m[1]] = true
		}
	}
	return types
}

// readCommitted loads one committed generated file and parses it.
func readCommitted(t *testing.T, root, rel string, parse func(string) map[string]map[string]bool) map[string]map[string]bool {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("reading committed %s: %v; run make client-dtos", rel, err)
	}
	return parse(string(content))
}

// walkWire descends a decoded wire body alongside the type graph. At every
// struct-shaped object it requires each key to be a field of the graph type
// and a wire name of every committed client type, then recurses into the
// field's type. Map keys are data, not contract names; raw payloads, enums
// and scalars are leaves.
func walkWire(t *testing.T, g *graph.Graph, targets []target, path string, v any, ref graph.TypeRef) {
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
			for _, tgt := range targets {
				if !tgt.serial[typ.Name][key] {
					t.Errorf("%s: wire name is missing from %s type %s in %s; run make client-dtos", keyPath, tgt.lang, typ.Name, tgt.path)
				}
			}
			walkWire(t, g, targets, keyPath, obj[key], field.Type)
		}
	case graph.KindMap:
		obj, ok := v.(map[string]any)
		if !ok {
			t.Errorf("%s: want an object for map, got %T", path, v)
			return
		}
		for _, key := range sortedKeys(obj) {
			walkWire(t, g, targets, path+"."+key, obj[key], *ref.Elem)
		}
	case graph.KindList:
		arr, ok := v.([]any)
		if !ok {
			t.Errorf("%s: want an array, got %T", path, v)
			return
		}
		for i, elem := range arr {
			walkWire(t, g, targets, fmt.Sprintf("%s[%d]", path, i), elem, *ref.Elem)
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
