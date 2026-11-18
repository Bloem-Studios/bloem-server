package apiv2

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// Test helpers Bloem's tests use, copied from Silo's tests before Silo pruned them
// (Silo #4465). Silo's test files no longer carry them.

type scanControlFixture struct {
	calls   int
	id      *int
	path    string
	failure error
}

func (*scanControlFixture) ScanControlAvailable() bool { return true }

func (f *scanControlFixture) StartLibraryScan(ctx context.Context, id *int, path string) (handlers.ScanAdmission, error) {
	f.calls++
	f.id = id
	f.path = path
	if claimsFrom(ctx) == nil {
		return handlers.ScanAdmission{}, &handlers.APIError{Status: 500, Message: "lost authority"}
	}
	return handlers.ScanAdmission{Status: "accepted", Mode: "file", LibraryID: 42}, f.failure
}

func (f *scanControlFixture) CancelLibraryScans(ctx context.Context, id int) (handlers.ScanCancellation, error) {
	f.calls++
	f.id = new(id)
	return handlers.ScanCancellation{Canceled: 2, LibraryID: id}, f.failure
}
