package transcodenode

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They are copied unchanged so the Silo files stay deleted.

import (
	"context"
)

func (s *stubRecipeStore) Delete(_ context.Context, sessionID string) error {
	s.deletes = append(s.deletes, sessionID)
	if s.delErr != nil {
		return s.delErr
	}
	s.card = nil
	s.ok = false
	return nil
}
