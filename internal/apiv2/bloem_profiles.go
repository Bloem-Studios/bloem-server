package apiv2

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
)

// bloemProfileLifecycle is the Bloem slice of ProfileService: binding a typed
// profile mutation to its Idempotency-Key lifecycle receipt.
type bloemProfileLifecycle interface {
	ProfileLifecycleRequest(ctx context.Context, key, method, routeID, profileID string, body []byte) (*lifecycleidempotency.Request, error)
}

type ProfileDeleteInput struct {
	ID             ID     `path:"id" doc:"The profile" example:"1"`
	IdempotencyKey string `header:"Idempotency-Key" doc:"Opaque lifecycle retry key"`
}
