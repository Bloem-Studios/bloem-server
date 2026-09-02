package digestfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const scopeDoc = `# Bloem Silo-compatible v1 Scope

Some preamble text that must be ignored.

## Breaking removals taken before lock

Intro paragraph that is skipped with the header rows.

| Removed | Release | Rationale |
|---|---|---|
| The legacy playback/start body   | protocol v3 | superseded |
| POST /api/v1/playback/transcode/start | protocol v3 | superseded |

## Next section

This must not be hashed.
`

func TestTableDigest(t *testing.T) {
	digest, err := TableDigest([]byte(scopeDoc))
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("TableDigest = %q", digest)
	}
	// Deterministic over identical input.
	if again, err := TableDigest([]byte(scopeDoc)); err != nil || again != digest {
		t.Fatalf("second call = %q, %v", again, err)
	}

	t.Run("formatting and prose do not move it", func(t *testing.T) {
		reframed := strings.Replace(scopeDoc, "Some preamble text that must be ignored.\n", "", 1)
		reframed = strings.Replace(reframed, "superseded |", "superseded |  ", 1)
		if got, err := TableDigest([]byte(reframed)); err != nil || got != digest {
			t.Errorf("prose/whitespace edits changed the digest: %q vs %q", got, digest)
		}
	})
	t.Run("an edited row moves it", func(t *testing.T) {
		edited := strings.Replace(scopeDoc, "| superseded |", "| superseded now |", 1)
		if got, err := TableDigest([]byte(edited)); err != nil || got == digest {
			t.Errorf("an edited removal row must change the digest (got %q)", got)
		}
	})
	t.Run("a new row moves it", func(t *testing.T) {
		added := strings.Replace(scopeDoc,
			"| POST /api/v1/playback/transcode/start | protocol v3 | superseded |",
			"| POST /api/v1/playback/transcode/start | protocol v3 | superseded |\n| A brand-new removal | 2026-09-02 | why |", 1)
		if got, err := TableDigest([]byte(added)); err != nil || got == digest {
			t.Errorf("a new removal row must change the digest (got %q)", got)
		}
	})
}

func TestTableDigestRejects(t *testing.T) {
	t.Run("missing section", func(t *testing.T) {
		if _, err := TableDigest([]byte("# v1 scope\n\nno removals here\n")); err == nil {
			t.Fatal("expected an error for a document without the removals section")
		}
	})
	t.Run("empty table", func(t *testing.T) {
		doc := "## Breaking removals taken before lock\n\n| Removed | Release | Rationale |\n|---|---|---|\n\n## next\n"
		if _, err := TableDigest([]byte(doc)); err == nil {
			t.Fatal("expected an error for a removals table with no rows")
		}
	})
}

func TestReadWriteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "digest.txt")
	pins := Pins{
		Contract:      "sha256:" + strings.Repeat("a", 64),
		RemovalsTable: "sha256:" + strings.Repeat("b", 64),
	}
	if err := Write(path, pins); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != pins {
		t.Errorf("round trip = %+v, want %+v", got, pins)
	}
}

func TestWriteRejects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "digest.txt")
	for _, pins := range []Pins{
		{Contract: "", RemovalsTable: "sha256:" + strings.Repeat("b", 64)},
		{Contract: "sha256:short", RemovalsTable: "sha256:" + strings.Repeat("b", 64)},
		{Contract: "md5:" + strings.Repeat("a", 64), RemovalsTable: "sha256:" + strings.Repeat("b", 64)},
		{Contract: "sha256:" + strings.Repeat("a", 64), RemovalsTable: ""},
	} {
		if err := Write(path, pins); err == nil {
			t.Errorf("Write accepted %+v", pins)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a rejected Write must not create the file")
	}
}

func TestReadRejects(t *testing.T) {
	for name, body := range map[string]string{
		"garbage line":     "contract: sha256:" + strings.Repeat("a", 64) + "\nsurprise: true\n",
		"missing table":    "contract: sha256:" + strings.Repeat("a", 64) + "\n",
		"missing contract": "removals-table: sha256:" + strings.Repeat("b", 64) + "\n",
		"malformed digest": "contract: nope\nremovals-table: sha256:" + strings.Repeat("b", 64) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "digest.txt")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(path); err == nil {
				t.Errorf("Read accepted %q", body)
			}
		})
	}
}
