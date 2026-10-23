package apiv2

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They are copied unchanged so the Silo files stay deleted.

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/ebookconvert"
)

type ebookFileConverter struct {
	calls  int
	result string
	err    error
}

func (c *ebookFileConverter) GetOrConvert(context.Context, string, ebookconvert.SourceKey) (string, error) {
	c.calls++
	return c.result, c.err
}

func (c *ebookFileConverter) Lookup(ebookconvert.SourceKey) (string, error, bool) {
	return "", nil, false
}
