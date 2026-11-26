package bloemsource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/buildinfo"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"

func TestNewNeedsADirectoryAndACleanCommit(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		dir  string
		info buildinfo.Info
	}{
		"no directory":     {"", buildinfo.Info{Revision: testRevision}},
		"blank directory":  {"  ", buildinfo.Info{Revision: testRevision}},
		"dirty build":      {dir, buildinfo.Info{Revision: testRevision, Dirty: true}},
		"no revision":      {dir, buildinfo.Info{}},
		"short revision":   {dir, buildinfo.Info{Revision: testRevision[:9]}},
		"unknown revision": {dir, buildinfo.Info{Revision: "unknown"}},
	}
	for name, tc := range cases {
		if offer := New(tc.dir, tc.info); offer != nil {
			t.Errorf("%s: New = %+v, want nil", name, offer)
		}
	}
	offer := New(dir, buildinfo.Info{Revision: strings.ToUpper(testRevision)})
	if offer == nil || offer.Revision() != testRevision {
		t.Fatalf("New = %+v, want the lower-case revision", offer)
	}
}

func TestArchiveIsTheRunningRevisionsFile(t *testing.T) {
	dir := t.TempDir()
	offer := New(dir, buildinfo.Info{Revision: testRevision})

	if _, err := offer.Archive(); err == nil {
		t.Fatal("Archive with no file: want an error")
	}
	// Another revision's archive is not this build's source.
	writeFile(t, filepath.Join(dir, ArchiveName("fedcba9876543210fedcba9876543210fedcba98")), "other")
	if _, err := offer.Archive(); err == nil {
		t.Fatal("Archive with only another revision's file: want an error")
	}
	writeFile(t, filepath.Join(dir, ArchiveName(testRevision)), "")
	if _, err := offer.Archive(); err == nil {
		t.Fatal("Archive with an empty file: want an error")
	}
	writeFile(t, filepath.Join(dir, ArchiveName(testRevision)), "source")
	archive, err := offer.Archive()
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archive.Name != "bloem-server-"+testRevision+".tar.gz" || archive.Size != int64(len("source")) ||
		archive.Path != filepath.Join(dir, archive.Name) {
		t.Fatalf("Archive = %+v", archive)
	}
}

func TestArchiveRejectsADirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ArchiveName(testRevision)), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, buildinfo.Info{Revision: testRevision}).Archive(); err == nil {
		t.Fatal("Archive naming a directory: want an error")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
