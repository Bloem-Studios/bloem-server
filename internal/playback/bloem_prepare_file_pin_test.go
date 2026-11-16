package playback

// Bloem-owned test. Kept out of Silo's own test files so upstream merges do not conflict here;
// see contracts/seams.txt.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

const prepareFileRetryPinFailure = "Silo changed PrepareFile's retries; follow them in prepareSourceFile (bloem_prepare_file.go), which prepares native inputs"

// prepareFileRetryLoopSteps are the calls PrepareFile's CPU-decode retry loop
// makes. prepareSourceFile must make each as often as PrepareFile does.
var prepareFileRetryLoopSteps = []string{"AdvanceAfterFailure", "Current", "Enabled", "releaseHWDevice"}

// TestPrepareSourceFileFollowsPrepareFileRetries keeps Bloem's copy of
// PrepareFile for native inputs on Silo's retries. A native input never
// reaches PrepareFile's own loop, so a retry Silo adds there and the copy
// misses fails native conversions without a sign. The test needs no FFmpeg:
// it reads the source.
func TestPrepareSourceFileFollowsPrepareFileRetries(t *testing.T) {
	fail := func(format string, args ...any) {
		t.Helper()
		t.Logf(format, args...)
		t.Fatal(prepareFileRetryPinFailure)
	}

	silo := prepareFilePinCalls(t, "prepare_file.go", "PrepareFile")
	bloem := prepareFilePinCalls(t, "bloem_prepare_file.go", "prepareSourceFile")
	run := prepareFilePinCalls(t, "bloem_prepare_file.go", "runSourcePrepare")
	if len(silo) == 0 || len(bloem) == 0 || len(run) == 0 {
		fail("PrepareFile, prepareSourceFile or runSourcePrepare is missing or calls nothing")
		return
	}

	// Every retry constructor or rule PrepareFile calls: StartupRetryHWAccel,
	// NewSoftwareDecodeRetryPipeline and whatever Silo adds next.
	for name := range silo {
		if strings.Contains(name, "Retry") && bloem[name] == 0 {
			fail("PrepareFile calls %s and prepareSourceFile does not", name)
		}
	}
	for _, name := range prepareFileRetryLoopSteps {
		if silo[name] != bloem[name] {
			fail("PrepareFile calls %s %d times, prepareSourceFile %d", name, silo[name], bloem[name])
		}
	}
	// PrepareFile classifies each failure from FFmpeg's retained stderr; a
	// native run classifies from its private tail instead.
	if silo["classifyFFmpegFailure"] > 0 && run["classify"] == 0 {
		fail("PrepareFile classifies FFmpeg failures and runSourcePrepare does not")
	}
}

// prepareFilePinCalls counts the calls in the named top-level function of
// file, closures included, by the called function's or method's name.
func prepareFilePinCalls(t *testing.T, file, function string) map[string]int {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Logf("parse %s: %v", file, err)
		t.Fatal(prepareFileRetryPinFailure)
	}
	calls := map[string]int{}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != function || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch callee := call.Fun.(type) {
			case *ast.Ident:
				calls[callee.Name]++
			case *ast.SelectorExpr:
				calls[callee.Sel.Name]++
			}
			return true
		})
	}
	return calls
}
