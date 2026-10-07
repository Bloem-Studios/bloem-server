//go:build integration

package nativestorage

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// IntegrationLibraryCommit returns an independent copy using only the existing
// per-instance commit hook. Callers install it before publishing their router.
func IntegrationLibraryCommit(s *LibraryManagement, commit func(context.Context, pgx.Tx, string) error) *LibraryManagement {
	copy := *s
	copy.commit = commit
	return &copy
}
