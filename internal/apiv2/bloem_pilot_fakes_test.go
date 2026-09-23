package apiv2

// Bloem-only methods on Silo's pilot fakes (pilot_fakes_test.go).

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
)

func (f *fakeProfiles) ProfileLifecycleRequest(context.Context, string, string, string, string, []byte) (*lifecycleidempotency.Request, error) {
	return nil, nil
}
