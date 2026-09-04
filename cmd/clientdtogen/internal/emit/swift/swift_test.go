package swift

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/emit"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/emit/kotlin"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph/graphtest"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
	clientv1 "github.com/Silo-Server/silo-server/contracts/client/v1"
)

var update = flag.Bool("update", false, "rewrite the golden files under cmd/clientdtogen/testdata/swift")

const goldenDir = "cmd/clientdtogen/testdata/swift"

// fixtureOptions are fixed so the golden output is reproducible; the revision
// is a recognizable fake, never a real SHA.
var fixtureOptions = emit.Options{
	ServerRevision: "0000000000000000000000000000000000000000",
	RegistryPath:   "cmd/clientdtogen/internal/graph/graphtest/graphtest.go",
}

func emitFixture(t *testing.T) emit.Files {
	t.Helper()
	files, err := Emitter{}.Emit(graphtest.Build(t, nil), fixtureOptions)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return files
}

func TestGolden(t *testing.T) {
	files := emitFixture(t)
	root := filepath.Join(graphtest.RepoRoot(t), goldenDir)
	if *update {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		if err := files.Write(root); err != nil {
			t.Fatal(err)
		}
	}
	wantPaths := map[string]bool{"fixture/Fixture.swift": true, "other/Other.swift": true, ContractFile: true}
	if len(files) != len(wantPaths) {
		t.Fatalf("emitted %d files, want %d", len(files), len(wantPaths))
	}
	for _, f := range files {
		if !wantPaths[f.Path] {
			t.Errorf("unexpected file %s", f.Path)
		}
		want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatalf("%s: %v (run with -update to write goldens)", f.Path, err)
		}
		if !bytes.Equal(want, f.Content) {
			t.Errorf("%s differs from golden (run with -update after reviewing):\n%s", f.Path, diffHint(want, f.Content))
		}
	}
}

func diffHint(want, got []byte) string {
	wl, gl := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	for i := range wl {
		if i >= len(gl) || wl[i] != gl[i] {
			g := "<eof>"
			if i < len(gl) {
				g = gl[i]
			}
			return "line " + strconv.Itoa(i+1) + ":\n  golden: " + wl[i] + "\n  got:    " + g
		}
	}
	return "golden is a prefix of the output"
}

func TestDeterminism(t *testing.T) {
	a := emitFixture(t)
	b := emitFixture(t)
	if !a.Equal(b) {
		t.Fatal("two emits over the fixture differ")
	}
}

var (
	typeDecl = regexp.MustCompile("^ *public (struct|enum) (`?\\w+`?)")
	anyDecl  = regexp.MustCompile(`\b(struct|enum|let|var|init|func)\b`)
	// Every declaration line the emitter writes is one of these forms. A line
	// that names a declaration keyword and matches none of them is a
	// provenance failure, not a style nit.
	allowedDecl = regexp.MustCompile("^ *(public (struct|enum) `?\\w+`?|public (let|var) \\w+|public static let \\w+|private let _\\w+|public init|public func encode\\(to encoder: any Encoder\\) throws \\{|case \\w+ = \"|let container = try decoder|var container = encoder|self\\.)")
	funcToken   = regexp.MustCompile(`\bfunc\b`)
	docLine     = regexp.MustCompile("^    /// Wire type `([^`]+)`\\. Direction: (request|response|both)\\. Dialect: (upstream-compat|bloem)\\.")
	propLine    = regexp.MustCompile(`^        public (?:let|var) (\w+): `)
	caseLine    = regexp.MustCompile(`^            case (\w+) = "([^"]*)"$`)
	// goldenTypes is every fixture type the emitter must render, the same list
	// the Kotlin golden test pins.
	goldenTypes = []string{"Scalars", "Child", "Collections", "Inner", "Embedded", "Response", "Request", "Shared", "Gated", "Gated2", "GatedChild", "GatedOnly", "Compat", "PromoCard", "BloemOnly", "Protocol", "Target", "Standalone"}
)

// TestProvenance asserts the §2/§8 guarantees over the fixture output: every
// declaration is explicitly public (bar the private indirection box), the only
// func is the mechanical encode(to:), every type name is a Go type name, and
// every property is backed by its own CodingKeys case carrying the wire name.
func TestProvenance(t *testing.T) {
	g := graphtest.Build(t, nil)
	goNames := map[string]bool{}
	for _, typ := range g.Types() {
		goNames[typ.Name] = true
	}
	files := emitFixture(t)
	types := map[string]bool{}
	for _, f := range files {
		lines := strings.Split(string(f.Content), "\n")
		// Per type: the properties declared and the CodingKeys cases seen.
		props := map[string]bool{}
		cases := map[string]bool{}
		current, vocabulary := "", false
		flush := func() {
			// A vocabulary type is a RawRepresentable String wrapper: it
			// decodes as a bare string and has no keys of its own.
			if !vocabulary {
				for name := range props {
					if !cases[name] {
						t.Errorf("%s: property %s.%s has no CodingKeys case", f.Path, current, name)
					}
				}
			}
			props, cases = map[string]bool{}, map[string]bool{}
		}
		for i, line := range lines {
			if funcToken.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "///") &&
				strings.TrimSpace(line) != "public func encode(to encoder: any Encoder) throws {" {
				t.Errorf("%s:%d: generated file contains a func other than encode(to:): %s", f.Path, i+1, line)
			}
			if anyDecl.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "//") && !allowedDecl.MatchString(line) {
				t.Errorf("%s:%d: declaration outside the generated forms: %s", f.Path, i+1, line)
			}
			if m := typeDecl.FindStringSubmatch(line); m != nil {
				name := strings.Trim(m[2], "`")
				if m[1] == "struct" {
					flush()
					current, vocabulary = name, strings.Contains(line, "RawRepresentable")
					types[name] = true
					if !goNames[name] {
						t.Errorf("%s:%d: type %s is not a Go type name", f.Path, i+1, name)
					}
					if !docLine.MatchString(lines[i-1]) {
						t.Errorf("%s:%d: type %s lacks the Wire type doc line", f.Path, i+1, name)
					}
				}
			}
			if m := propLine.FindStringSubmatch(line); m != nil {
				props[m[1]] = true
			}
			if m := caseLine.FindStringSubmatch(line); m != nil {
				cases[m[1]] = true
			}
		}
		flush()
	}
	for _, name := range goldenTypes {
		if !types[name] {
			t.Errorf("fixture type %s was not emitted", name)
		}
	}
}

// TestFixtureShapes spot-checks the §4.4 rows in the fixture output so a
// golden regression reads as a rule violation, not a byte diff. Each want is
// the Kotlin test's line translated by the mapping this emitter documents.
func TestFixtureShapes(t *testing.T) {
	files := emitFixture(t)
	var fixture, other, contract string
	for _, f := range files {
		switch f.Path {
		case "fixture/Fixture.swift":
			fixture = string(f.Content)
		case "other/Other.swift":
			other = string(f.Content)
		case ContractFile:
			contract = string(f.Content)
		}
	}
	for _, want := range []string{
		// response-reachable scalars: zero defaults; pointers optional; Raw always optional
		"        public let i64: Int64\n",
		"            i64: Int64 = 0",
		"            self.i64 = try container.decodeIfPresent(Int64.self, forKey: .i64) ?? 0\n",
		"        public let tp: String?\n",
		"            tp: String? = nil",
		"            self.tp = try container.decodeIfPresent(String.self, forKey: .tp)\n",
		"            u: String = \"\"",
		"        public let raw: BloemJSONValue?\n",
		"        public let anyMap: [String: BloemJSONValue]\n",
		"            anyMap: [String: BloemJSONValue] = [:]",
		"            bytes: String = \"\"",
		"            proto: `Protocol` = `Protocol`(rawValue: \"\")",
		"        public let protoPtr: `Protocol`?\n",
		"        public let intPtr: Int?\n",
		// collections
		"            optionalList: [Child]? = nil",
		"            structMap: [String: Child] = [:]",
		"            fixed: [Int] = []",
		"            nested: [[String]] = []",
		"            ptrList: [Child?] = []",
		"            structOmit: Child = Child()",
		// request-only: required unless omitempty/pointer; promoted fields inline
		// at the embed point in the surviving declaration order.
		"        public let id: Int64\n        public let deep: Inner\n        public let kind: String\n",
		"            self.id = try container.decode(Int64.self, forKey: .id)\n",
		"            zero: Child? = nil",
		"        public let direct: Child\n",
		"            ptr: Child? = nil",
		"            shared: Shared,\n            proto: `Protocol`\n",
		// self-reference: Swift cannot store it inline, so it goes through a box
		"        /// Self-referential on the wire; stored through a box.\n        public var next: Child? { _next.value }\n        private let _next: Indirect<Child?>\n",
		"            self._next = Indirect(try container.decodeIfPresent(Child.self, forKey: .next))\n",
		"            try container.encodeIfPresent(self.next, forKey: .next)\n",
		// self-qualified so a field called \"container\" cannot shadow the encoder's.
		"            try container.encode(self.name, forKey: .name)\n",
		// serializer escape hatch and alias resolution
		"        public let rate: FrameRateWire?\n",
		"        public let ratePtr: FrameRateWire?\n",
		"            target: Other.Target = Other.Target()",
		// dialect marking and gates
		"        /// Dialect: bloem.\n        public let promo: PromoCard?\n",
		"    /// Wire type `" + graphtest.FixturePath + ".GatedOnly`. Direction: response. Dialect: upstream-compat. Gate: cap.gated|cap.other.\n",
		"    /// Wire type `" + graphtest.FixturePath + ".Gated`. Direction: response. Dialect: upstream-compat. Gate: cap.gated. Registered root.\n",
		// keyword repair and the wire names that never change
		"            case proto = \"proto\"\n",
		// enum
		"    public struct `Protocol`: RawRepresentable, Codable, Hashable, Sendable {\n        public let wire: String\n        public var rawValue: String { wire }\n        public init(rawValue: String) { self.wire = rawValue }\n",
		"        public static let HLS: `Protocol` = `Protocol`(rawValue: \"hls\")\n",
		"        public static let KNOWN: [`Protocol`] = [HLS, PROGRESSIVE, LATE]\n",
	} {
		if !strings.Contains(fixture, want) {
			t.Errorf("Fixture.swift lacks:\n%s", want)
		}
	}
	if !strings.Contains(other, "public enum Other {\n") || !strings.Contains(other, "    public struct Standalone: Codable, Hashable, Sendable {\n        public let only: String\n") {
		t.Errorf("Other.swift shape wrong:\n%s", other)
	}
	if strings.Contains(other, "FrameRate") {
		t.Error("Other.swift emitted FrameRate, which only a registry serializer may represent")
	}
	for _, want := range []string{
		"public static let SERVER_REVISION: String = \"0000000000000000000000000000000000000000\"\n",
		"public static let GENERATOR_VERSION: Int = 1\n",
		"public static let CONTRACT_DIGEST: String = \"sha256:",
		"        \"Fixture.GatedOnly\": \"cap.gated|cap.other\",\n",
		"        \"Fixture.Shared\": \"both\",\n",
		"        \"Fixture.PromoCard\": \"upstream-compat\",\n",
		"        \"Other.Target\": \"upstream-compat\",\n",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("GeneratedContract.swift lacks:\n%s", want)
		}
	}
}

// TestContractDigestMatchesKotlin pins the one value both emitters must
// agree on: the digest is of the type graph, not of a rendering, so a
// Swift-only drift in it would mean the two clients had been told they were
// looking at different contracts.
func TestContractDigestMatchesKotlin(t *testing.T) {
	g := graphtest.Build(t, nil)
	swiftFiles, swiftErr := Emitter{}.Emit(g, fixtureOptions)
	kotlinFiles, kotlinErr := kotlin.Emitter{}.Emit(g, fixtureOptions)
	swiftDigest := digestOf(t, swiftFiles, swiftErr)
	kotlinDigest := digestOf(t, kotlinFiles, kotlinErr)
	if swiftDigest == "" {
		t.Fatal("no CONTRACT_DIGEST in the Swift contract file")
	}
	if swiftDigest != kotlinDigest {
		t.Errorf("contract digest differs between emitters:\n  swift:  %s\n  kotlin: %s", swiftDigest, kotlinDigest)
	}
}

// digestOf pulls the sha256 out of whichever contract file the emitter wrote.
func digestOf(t *testing.T, files emit.Files, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !strings.HasPrefix(f.Path, "GeneratedContract.") {
			continue
		}
		for _, line := range strings.Split(string(f.Content), "\n") {
			if i := strings.Index(line, "sha256:"); i >= 0 {
				return strings.Trim(line[i:], `"`)
			}
		}
	}
	return ""
}

// TestRefusesUnusableSerializer proves the escape hatch fails loudly: an entry
// that names neither a swift target nor a kotlin one to take the type name
// from cannot be rendered, and the error names the field.
func TestRefusesUnusableSerializer(t *testing.T) {
	reg := graphtest.Registry()
	reg.Serializers[graphtest.FixturePath+".Response.frame_rate"] = registry.Serializer{"typescript": "FrameRateWire"}
	_, err := Emitter{}.Emit(graphtest.Build(t, reg), fixtureOptions)
	if err == nil || !strings.Contains(err.Error(), "Response.Rate") {
		t.Fatalf("Emit error = %v, want the field named", err)
	}
}

// TestFallsBackToKotlinSerializerName pins the documented fallback: the
// fixture registry gives frame_rate_ptr a kotlin target only, and the Swift
// type is that target's last component.
func TestFallsBackToKotlinSerializerName(t *testing.T) {
	files := emitFixture(t)
	for _, f := range files {
		if f.Path != "fixture/Fixture.swift" {
			continue
		}
		if !strings.Contains(string(f.Content), "public let ratePtr: FrameRateWire?") {
			t.Error("frame_rate_ptr did not take its Swift type from the kotlin target")
		}
		return
	}
	t.Fatal("no fixture/Fixture.swift")
}

func TestRefusesConstantNamedKnown(t *testing.T) {
	g := graphtest.Build(t, nil)
	typ, ok := g.Type(graphtest.FixturePath + ".Protocol")
	if !ok {
		t.Fatal("Protocol not in graph")
	}
	typ.Constants = append(typ.Constants, graph.Constant{GoName: "ProtocolKnown", Value: "known"})
	_, err := Emitter{}.Emit(g, fixtureOptions)
	if err == nil || !strings.Contains(err.Error(), "ProtocolKnown") {
		t.Fatalf("Emit error = %v, want the constant named", err)
	}
}

// TestRefusesNamespaceCollision proves the namespace guard: a type whose name
// is a namespace would silently shadow it at every cross-package reference.
func TestRefusesNamespaceCollision(t *testing.T) {
	g := graphtest.Build(t, nil)
	typ, ok := g.Type(graphtest.FixturePath + ".Shared")
	if !ok {
		t.Fatal("Shared not in graph")
	}
	typ.Name = "Other"
	_, err := Emitter{}.Emit(g, fixtureOptions)
	if err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Fatalf("Emit error = %v, want the namespace collision named", err)
	}
}

// specTimelineV3 is the §2.1 "after" block as this emitter renders it: the
// contract for the real type, in Swift.
const specTimelineV3 = "    /// Wire type `internal/playback.TimelineV3`. Direction: response. Dialect: upstream-compat.\n" +
	`    public struct TimelineV3: Codable, Hashable, Sendable {
        public let sourceStartSeconds: Double
        public let streamOriginSeconds: Double
        public let playerStartSeconds: Double
        public let timelineOffsetSeconds: Double
        public let seekWindowStartSeconds: Double?
        public let seekWindowEndSeconds: Double?
        public let canSeekAnywhere: Bool
        public let seekRestoration: String

        public enum CodingKeys: String, CodingKey {
            case sourceStartSeconds = "source_start_seconds"
            case streamOriginSeconds = "stream_origin_seconds"
            case playerStartSeconds = "player_start_seconds"
            case timelineOffsetSeconds = "timeline_offset_seconds"
            case seekWindowStartSeconds = "seek_window_start_seconds"
            case seekWindowEndSeconds = "seek_window_end_seconds"
            case canSeekAnywhere = "can_seek_anywhere"
            case seekRestoration = "seek_restoration"
        }
`

func emitPlayback(t *testing.T) emit.Files {
	t.Helper()
	raw, err := clientv1.FS.ReadFile("registry.json")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(graph.Config{Dir: graphtest.RepoRoot(t), Registry: reg})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	files, err := Emitter{}.Emit(g, emit.Options{ServerRevision: "deadbeef", RegistryPath: "contracts/client/v1/registry.json"})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return files
}

// TestPlaybackRegistry emits the real registry: it must succeed, be stable
// across two runs, and render TimelineV3 exactly as §2.1 promises.
func TestPlaybackRegistry(t *testing.T) {
	files := emitPlayback(t)
	again := emitPlayback(t)
	if !files.Equal(again) {
		t.Fatal("two emits over the real registry differ")
	}
	var playback string
	for _, f := range files {
		if f.Path == "playback/Playback.swift" {
			playback = string(f.Content)
		}
	}
	if playback == "" {
		t.Fatalf("no playback/Playback.swift among %d files", len(files))
	}
	start := strings.Index(playback, "    /// Wire type `internal/playback.TimelineV3`")
	if start < 0 {
		t.Fatal("TimelineV3 not emitted")
	}
	block := playback[start:]
	if !strings.HasPrefix(block, specTimelineV3) {
		end := start + len(specTimelineV3)
		if end > len(playback) {
			end = len(playback)
		}
		t.Errorf("TimelineV3 differs from the §2.1 contract:\n%s", playback[start:end])
	}
}

const supportDir = "cmd/clientdtogen/testdata/swiftsupport"

// TestGeneratedSwiftCompilesAndDecodes is the gate the golden text cannot
// give: swiftc type-checks the committed fixture output against the
// client-owned support types, and the driver decodes bodies the Go types can
// produce, asserting the §4.4 rules at runtime — absent keys taking defaults,
// a null slice coercing to empty, an unknown enum value surviving, the
// self-referential type round-tripping, and a request-only type refusing a
// body with a required key missing.
//
// It skips where no Swift toolchain exists (Go CI on Linux); the Apple client
// repository runs the same compilation on every vendored update.
func TestGeneratedSwiftCompilesAndDecodes(t *testing.T) {
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("no swiftc on PATH; the generated Swift is checked by text here and compiled in bloem-apple-v3")
	}
	root := graphtest.RepoRoot(t)
	bin := filepath.Join(t.TempDir(), "conformance")
	args := []string{"-swift-version", "6", "-o", bin,
		filepath.Join(root, goldenDir, "fixture", "Fixture.swift"),
		filepath.Join(root, goldenDir, "other", "Other.swift"),
		filepath.Join(root, goldenDir, ContractFile),
		filepath.Join(root, supportDir, "Support.swift"),
		filepath.Join(root, supportDir, "main.swift"),
	}
	if out, err := exec.Command(swiftc, args...).CombinedOutput(); err != nil {
		t.Fatalf("swiftc rejected the generated fixture: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("the generated Swift did not decode as specified: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "swift conformance: ok") {
		t.Fatalf("conformance driver did not report success:\n%s", out)
	}
}

// TestCommittedSwiftCompiles type-checks the whole committed output, not just
// the fixture. The real registry reaches shapes the synthetic package does
// not, and the first run of this check found one: a wire field named
// `container` shadowed the encoding container in encode(to:). Text assertions
// cannot see that; a compiler can.
func TestCommittedSwiftCompiles(t *testing.T) {
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("no swiftc on PATH; the committed Swift is compiled in bloem-apple-v3")
	}
	root := graphtest.RepoRoot(t)
	var sources []string
	if err := filepath.WalkDir(filepath.Join(root, "contracts", "client", "v1", "swift"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".swift") {
			sources = append(sources, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walking the committed Swift: %v; run make client-dtos", err)
	}
	if len(sources) == 0 {
		t.Fatal("no committed Swift under contracts/client/v1/swift; run make client-dtos")
	}
	sort.Strings(sources)
	args := append([]string{"-swift-version", "6", "-typecheck"}, sources...)
	args = append(args,
		filepath.Join(root, supportDir, "Support.swift"),
		filepath.Join(root, supportDir, "RegistrySerializers.swift"),
	)
	if out, err := exec.Command(swiftc, args...).CombinedOutput(); err != nil {
		t.Fatalf("swiftc rejected the committed client DTOs: %v\n%s", err, out)
	}
}
