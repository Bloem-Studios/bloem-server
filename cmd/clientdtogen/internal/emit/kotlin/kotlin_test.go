package kotlin

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/emit"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph/graphtest"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
	clientv1 "github.com/Silo-Server/silo-server/contracts/client/v1"
)

var update = flag.Bool("update", false, "rewrite the golden files under cmd/clientdtogen/testdata/kotlin")

const goldenDir = "cmd/clientdtogen/testdata/kotlin"

// fixtureOptions are fixed so the golden output is reproducible; the revision
// is a recognisable fake, never a real SHA.
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
	wantPaths := map[string]bool{"fixture/Fixture.kt": true, "other/Other.kt": true, ContractFile: true}
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
			return "line " + itoa(i+1) + ":\n  golden: " + wl[i] + "\n  got:    " + g
		}
	}
	return "golden is a prefix of the output"
}

func itoa(i int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+i%10)) + "") }

func TestDeterminism(t *testing.T) {
	a := emitFixture(t)
	b := emitFixture(t)
	if !a.Equal(b) {
		t.Fatal("two emits over the fixture differ")
	}
}

var (
	valLine       = regexp.MustCompile(`^\s*public val `)
	anyVal        = regexp.MustCompile(`\bval\b`)
	classDecl     = regexp.MustCompile(`^public (data class|class|value class|object) (\w+)`)
	anyClassDecl  = regexp.MustCompile(`\b(data class|class|object)\b`)
	serialNameRe  = regexp.MustCompile(`^\s*@SerialName\("[^"]*"\)$`)
	withSerialRe  = regexp.MustCompile(`^\s*@Serializable\(with = \w+\.Serializer::class\)$`)
	funToken      = regexp.MustCompile(`\bfun\b`)
	kdocLine      = regexp.MustCompile("^/\\*\\* Wire type `([^`]+)`\\. Direction: (request|response|both)\\. Dialect: (upstream-compat|bloem)\\.")
	goldenClasses = []string{"Scalars", "Child", "Collections", "Base", "Inner", "Embedded", "Response", "Request", "Shared", "Gated", "Gated2", "GatedChild", "GatedOnly", "Compat", "PromoCard", "BloemOnly", "Protocol", "Mixin", "Target", "Standalone"}
)

// TestProvenance asserts the §2/§8 guarantees over the fixture output: every
// property line is `public val` preceded by its own @SerialName line, every
// declaration is public, no fun anywhere, every class name is a Go type name.
func TestProvenance(t *testing.T) {
	g := graphtest.Build(t, nil)
	goNames := map[string]bool{}
	for _, typ := range g.Types() {
		goNames[typ.Name] = true
	}
	files := emitFixture(t)
	classes := map[string]bool{}
	for _, f := range files {
		lines := strings.Split(string(f.Content), "\n")
		for i, line := range lines {
			if funToken.MatchString(line) {
				t.Errorf("%s:%d: generated file contains fun: %s", f.Path, i+1, line)
			}
			if anyVal.MatchString(line) && !valLine.MatchString(line) && !strings.Contains(line, "(public val wire: String)") {
				t.Errorf("%s:%d: val without explicit public: %s", f.Path, i+1, line)
			}
			if anyClassDecl.MatchString(line) && !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "/**") && !strings.HasPrefix(line, "    /**") && !strings.HasPrefix(line, "    public companion object") && !classDecl.MatchString(line) {
				t.Errorf("%s:%d: declaration without explicit public: %s", f.Path, i+1, line)
			}
			if m := classDecl.FindStringSubmatch(line); m != nil && m[1] != "object" {
				classes[m[2]] = true
				if !goNames[m[2]] {
					t.Errorf("%s:%d: class %s is not a Go type name", f.Path, i+1, m[2])
				}
				if i == 0 || !strings.HasPrefix(lines[i-1], "@") {
					t.Errorf("%s:%d: class %s lacks a @Serializable line above it", f.Path, i+1, m[2])
				}
				if !kdocLine.MatchString(lines[i-2]) && !kdocLine.MatchString(lines[i-3]) {
					t.Errorf("%s:%d: class %s lacks the Wire type KDoc line", f.Path, i+1, m[2])
				}
			}
			if valLine.MatchString(line) && !strings.Contains(line, "public val KNOWN:") && f.Path != ContractFile {
				prev := lines[i-1]
				if withSerialRe.MatchString(prev) {
					prev = lines[i-2]
				}
				if !serialNameRe.MatchString(prev) {
					t.Errorf("%s:%d: property without its own @SerialName line: %s", f.Path, i+1, line)
				}
			}
		}
	}
	for _, name := range goldenClasses {
		if !classes[name] {
			t.Errorf("fixture type %s was not emitted", name)
		}
	}
}

// TestFixtureShapes spot-checks the §4.4 rows in the fixture output so a
// golden regression reads as a rule violation, not a byte diff.
func TestFixtureShapes(t *testing.T) {
	files := emitFixture(t)
	var fixture, other, contract string
	for _, f := range files {
		switch f.Path {
		case "fixture/Fixture.kt":
			fixture = string(f.Content)
		case "other/Other.kt":
			other = string(f.Content)
		case ContractFile:
			contract = string(f.Content)
		}
	}
	for _, want := range []string{
		// response-reachable scalars: zero defaults; pointers null; Raw always JsonElement?
		"    public val i64: Long = 0L,\n",
		"    public val tp: String? = null,\n",
		"    public val u: String = \"\",\n",
		"    public val raw: JsonElement? = null,\n",
		"    public val any: JsonElement? = null,\n",
		"    public val anyMap: Map<String, JsonElement> = emptyMap(),\n",
		"    public val bytes: String = \"\",\n",
		"    public val proto: Protocol = Protocol(\"\"),\n",
		"    public val protoPtr: Protocol? = null,\n",
		"    public val intPtr: Int? = null,\n",
		// collections
		"    public val optionalList: List<Child>? = null,\n",
		"    public val structMap: Map<String, Child> = emptyMap(),\n",
		"    public val fixed: List<Int> = emptyList(),\n",
		"    public val nested: List<List<String>> = emptyList(),\n",
		"    public val ptrList: List<Child?> = emptyList(),\n",
		"    public val structOmit: Child = Child(),\n",
		// request-only: required unless omitempty/pointer; promoted fields inline
		"public data class Embedded(\n    @SerialName(\"id\")\n    public val id: Long,\n    @SerialName(\"kind\")\n    public val kind: String,\n    @SerialName(\"deep\")\n    public val deep: Inner,\n",
		"    public val mixed: String,\n",
		"    public val low: String,\n",
		"    public val zero: Child? = null,\n",
		"    public val direct: Child,\n",
		"    public val ptr: Child? = null,\n",
		"public data class Request(\n    @SerialName(\"shared\")\n    public val shared: Shared,\n    @SerialName(\"proto\")\n    public val proto: Protocol,\n)\n",
		// serializer escape hatch and alias resolution
		"import org.bloemserver.bloem.contract.other.Target\n",
		"import org.example.FrameRateWire\n",
		"    @SerialName(\"frame_rate\")\n    @Serializable(with = FrameRateWire.Serializer::class)\n    public val rate: FrameRateWire? = null,\n",
		"    @SerialName(\"frame_rate_ptr\")\n    @Serializable(with = FrameRateWire.Serializer::class)\n    public val ratePtr: FrameRateWire? = null,\n",
		"    public val target: Target = Target(),\n",
		// dialect marking and gates
		"    /** Dialect: bloem. */\n    @SerialName(\"promo\")\n    public val promo: PromoCard? = null,\n",
		"/** Wire type `" + graphtest.FixturePath + ".GatedOnly`. Direction: response. Dialect: upstream-compat. Gate: cap.gated|cap.other. */\n",
		"/** Wire type `" + graphtest.FixturePath + ".Gated`. Direction: response. Dialect: upstream-compat. Gate: cap.gated. Registered root. */\n",
		// enum
		"public value class Protocol(public val wire: String) {\n    public companion object {\n        @SerialName(\"hls\")\n        public val HLS: Protocol = Protocol(\"hls\")\n",
		"        public val KNOWN: List<Protocol> = listOf(HLS, PROGRESSIVE, LATE)\n",
	} {
		if !strings.Contains(fixture, want) {
			t.Errorf("Fixture.kt lacks:\n%s", want)
		}
	}
	if !strings.Contains(other, "package org.bloemserver.bloem.contract.other\n") || !strings.Contains(other, "public data class Standalone(\n    @SerialName(\"only\")\n    public val only: String,\n)") {
		t.Errorf("Other.kt shape wrong:\n%s", other)
	}
	if strings.Contains(other, "FrameRate") {
		t.Error("Other.kt emitted FrameRate, which only a registry serializer may represent")
	}
	for _, want := range []string{
		"public const val SERVER_REVISION: String = \"0000000000000000000000000000000000000000\"\n",
		"public const val GENERATOR_VERSION: Int = 1\n",
		"public const val CONTRACT_DIGEST: String = \"sha256:",
		"        \"org.bloemserver.bloem.contract.fixture.GatedOnly\" to \"cap.gated|cap.other\",\n",
		"        \"org.bloemserver.bloem.contract.fixture.Shared\" to \"both\",\n",
		"        \"org.bloemserver.bloem.contract.fixture.PromoCard\" to \"bloem\",\n",
		"        \"org.bloemserver.bloem.contract.other.Target\" to \"upstream-compat\",\n",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("GeneratedContract.kt lacks:\n%s", want)
		}
	}
}

func TestRefusesMissingKotlinSerializer(t *testing.T) {
	reg := graphtest.Registry()
	reg.Serializers[graphtest.FixturePath+".Response.frame_rate_ptr"] = registry.Serializer{"swift": "FrameRateWire"}
	_, err := Emitter{}.Emit(graphtest.Build(t, reg), fixtureOptions)
	if err == nil || !strings.Contains(err.Error(), "Response.RatePtr") {
		t.Fatalf("Emit error = %v, want the field named", err)
	}
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

// v2TimelineLines are the hand-written bloem-android-v2 declaration lines for
// the same struct, quoted from docs/specs/client-dto-generator.md §2.1. None
// may reappear in the generated class: the only shared strings are the wire
// names inside @SerialName.
var v2TimelineLines = []string{
	`@Serializable`,
	`data class PlaybackTimelineV3(`,
	`@SerialName("source_start_seconds") val sourceStartSeconds: Double = 0.0,`,
	`@SerialName("stream_origin_seconds") val streamOriginSeconds: Double = 0.0,`,
	`@SerialName("player_start_seconds") val playerStartSeconds: Double = 0.0,`,
	`@SerialName("timeline_offset_seconds") val timelineOffsetSeconds: Double = 0.0,`,
	`@SerialName("seek_window_start_seconds") val seekWindowStartSeconds: Double? = null,`,
	`@SerialName("seek_window_end_seconds") val seekWindowEndSeconds: Double? = null,`,
	`@SerialName("can_seek_anywhere") val canSeekAnywhere: Boolean = true,`,
	`@SerialName("seek_restoration") val seekRestoration: String = "player_position",`,
	`)`,
}

// specTimelineV3 is the §2.1 "after" block: the contract for the real type.
const specTimelineV3 = "/** Wire type `internal/playback.TimelineV3`. Direction: response. Dialect: upstream-compat. */\n" +
	`@Serializable
public data class TimelineV3(
    @SerialName("source_start_seconds")
    public val sourceStartSeconds: Double = 0.0,
    @SerialName("stream_origin_seconds")
    public val streamOriginSeconds: Double = 0.0,
    @SerialName("player_start_seconds")
    public val playerStartSeconds: Double = 0.0,
    @SerialName("timeline_offset_seconds")
    public val timelineOffsetSeconds: Double = 0.0,
    @SerialName("seek_window_start_seconds")
    public val seekWindowStartSeconds: Double? = null,
    @SerialName("seek_window_end_seconds")
    public val seekWindowEndSeconds: Double? = null,
    @SerialName("can_seek_anywhere")
    public val canSeekAnywhere: Boolean = false,
    @SerialName("seek_restoration")
    public val seekRestoration: String = "",
)
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

// TestPlaybackRegistry emits the real starter registry: it must succeed, be
// stable across two runs, and render TimelineV3 exactly as §2.1 promises with
// no v2 declaration line surviving.
func TestPlaybackRegistry(t *testing.T) {
	files := emitPlayback(t)
	again := emitPlayback(t)
	if !files.Equal(again) {
		t.Fatal("two emits over internal/playback differ")
	}
	var playback string
	for _, f := range files {
		if f.Path == "playback/Playback.kt" {
			playback = string(f.Content)
		}
	}
	if playback == "" {
		t.Fatalf("no playback/Playback.kt among %d files", len(files))
	}
	start := strings.Index(playback, "/** Wire type `internal/playback.TimelineV3`")
	if start < 0 {
		t.Fatal("TimelineV3 not emitted")
	}
	end := strings.Index(playback[start:], "\n)\n")
	block := playback[start : start+end+3]
	if block != specTimelineV3 {
		t.Errorf("TimelineV3 differs from the §2.1 contract:\n%s", block)
	}
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, v2 := range v2TimelineLines {
			if trimmed == v2 && v2 != "@Serializable" && v2 != ")" {
				t.Errorf("generated line coincides with v2: %s", line)
			}
		}
	}
	if funToken.MatchString(playback) {
		t.Error("Playback.kt contains fun")
	}
}
