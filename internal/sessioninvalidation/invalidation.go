// Package sessioninvalidation provides the bounded post-commit context used by
// mandatory compatibility-session invalidation.
package sessioninvalidation

import (
	"context"
	"time"
)

// callbackTimeout is deliberately short: invalidation is a targeted database
// delete plus in-memory eviction, and must not extend a completed mutation
// request indefinitely when its backing store is unavailable.
const callbackTimeout = 5 * time.Second

// Run executes a mandatory post-commit invalidation independently of client
// cancellation while preserving request-scoped values such as trace IDs.
func Run(ctx context.Context, callback func(context.Context) error) error {
	return runWithTimeout(ctx, callbackTimeout, callback)
}

func runWithTimeout(ctx context.Context, timeout time.Duration, callback func(context.Context) error) error {
	detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	return callback(detachedCtx)
}
