package nativestorage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostApprovalsDefaultAndProtection(t *testing.T) {
	a, err := LoadApprovals("")
	if err != nil || len(a) != 0 {
		t.Fatalf("default approvals=%v err=%v", a, err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "approvals.json")
	if err = os.WriteFile(p, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadApprovals(p); err == nil {
		t.Fatal("accepted writable approvals")
	}
	if err = os.Chmod(p, 0400); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadApprovals(p); err != nil {
		t.Fatal(err)
	}
	link := p + ".link"
	if err = os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadApprovals(link); err == nil {
		t.Fatal("accepted symlink")
	}
	if _, err = LoadApprovals(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("ignored specified missing file")
	}
}
