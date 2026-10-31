package apiv2

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They follow Silo's current interfaces so the Silo files stay deleted.

import (
	"context"
	"os"

	"github.com/Silo-Server/silo-server/internal/ebookconvert"
)

// ebookFileConverter opens result, a path to an existing EPUB, as Silo's
// converter returns its cache entry open (Silo #3315).
type ebookFileConverter struct {
	calls  int
	result string
	err    error
}

func (c *ebookFileConverter) GetOrConvert(context.Context, string, ebookconvert.SourceKey) (*os.File, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return os.Open(c.result)
}

func (c *ebookFileConverter) Lookup(ebookconvert.SourceKey) (*os.File, error, bool) {
	return nil, nil, false
}
