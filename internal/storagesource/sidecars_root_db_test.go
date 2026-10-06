//go:build integration

package storagesource

import (
	"fmt"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"testing"
)

func TestNativeSidecarCandidatesDistinctLiteralRootPrefixes(t *testing.T) {
	prefixes := []string{"", "/", "//", "dir/", "dir//"}
	var entries []*storagev1.Entry
	for i, prefix := range prefixes {
		entries = append(entries, siblingEntry(fmt.Sprintf("book-%d", i), "Book.epub", prefix+"Book.epub"), siblingEntry(fmt.Sprintf("cover-%d", i), "Book.jpg", prefix+"Book.jpg"))
	}
	r, _, lease, _ := siblingFixture(t, entries...)
	for i, prefix := range prefixes {
		t.Run(fmt.Sprintf("prefix-%d", i), func(t *testing.T) {
			got, single, err := r.SiblingCandidates(t.Context(), lease, prefix, []string{"book.jpg"}, []string{".epub"})
			if err != nil || !single || len(got) != 1 || got[0].Id != fmt.Sprintf("cover-%d", i) || got[0].LogicalPath != prefix+"Book.jpg" {
				t.Fatalf("literal prefix %q conflated: entries=%v single=%v err=%v", prefix, got, single, err)
			}
		})
	}
}
