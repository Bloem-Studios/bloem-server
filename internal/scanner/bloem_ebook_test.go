package scanner

// Bloem ebook group-key repair coverage moved out of Silo's ebook_test.go.

import (
	"testing"
)

func TestEbookGroupKeyRepairDoesNotRequeueRemoteEnrichment(t *testing.T) {
	if shouldEnqueueEbookEnrichment(true) {
		t.Fatal("group-key repair should not requeue remote ebook enrichment")
	}
	if !shouldEnqueueEbookEnrichment(false) {
		t.Fatal("ordinary ebook reconciliation should queue remote enrichment")
	}
}
