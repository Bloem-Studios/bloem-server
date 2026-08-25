package livetv

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const prairieSourceCommit = "095ecd22fbea3384a905eb9049386015db3ff4d8"

func TestPrairieSourceManifest(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	manifestPath := filepath.Join(root, "docs", "livetv", "prairie-source-manifest.tsv")
	f, err := os.Open(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // read-only test fixture cleanup

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() || scanner.Text() != "target_path\tsource_path\tsource_blob\tclassification\trationale" {
		t.Fatal("unexpected Prairie source manifest header")
	}
	seen := map[string]bool{}
	rows := 0
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 5 {
			t.Fatalf("manifest row %d has %d fields", rows+2, len(fields))
		}
		target, source, blob, class, rationale := fields[0], fields[1], fields[2], fields[3], fields[4]
		if target == "" || source == "" || rationale == "" {
			t.Fatalf("manifest row %d has an empty required field", rows+2)
		}
		if seen[target] {
			t.Fatalf("duplicate target path %q", target)
		}
		seen[target] = true
		switch class {
		case "imported", "adapted", "omitted", "bloem-created":
		default:
			t.Fatalf("target %q has invalid classification %q", target, class)
		}
		if class != "omitted" {
			if info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(target))); statErr != nil || info.IsDir() {
				t.Fatalf("manifest target %q does not exist as a file", target)
			}
		}
		if class == "bloem-created" {
			if source != "-" || blob != "-" {
				t.Fatalf("Bloem-created target %q must not claim a Prairie blob", target)
			}
		} else if len(blob) != 40 || strings.Trim(blob, "0123456789abcdef") != "" {
			t.Fatalf("target %q has invalid Git blob SHA %q", target, blob)
		}
		rows++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if rows < 90 {
		t.Fatalf("manifest has only %d classified Prairie paths", rows)
	}

	notices, err := os.ReadFile(filepath.Join(root, "THIRD_PARTY_NOTICES.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(notices), prairieSourceCommit) ||
		!strings.Contains(string(notices), "Prairie-Server/prairie-server") {
		t.Fatal("third-party notice does not pin the Prairie source revision")
	}
}
