// Package mediasource supplies seekable files from authorized local paths or
// native storage plugins. It does not grant catalog or library access.
package mediasource

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

type Ref struct{ SourceID, EntryID, Revision string }
type Info struct {
	Name, LogicalPath, Revision string
	Size                        int64
	ModifiedAt                  time.Time
}
type Source interface {
	Stat(context.Context, Ref) (Info, error)
	ReadRange(context.Context, Ref, int64, int64, io.Writer) error
}
type File interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
	Info() Info
}

const maxRange = int64(8 << 20)
const maxChunk = 128 << 10

func validateText(value string, limit int, required bool) error {
	if (required && value == "") || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("invalid storage metadata")
	}
	return nil
}
func validateRef(ref Ref) error {
	for _, id := range []string{ref.SourceID, ref.EntryID} {
		if err := validateText(id, 1024, true); err != nil {
			return err
		}
	}
	return validateText(ref.Revision, 4096, true)
}
func validateInfo(info Info) error {
	if info.Size < 0 || strings.TrimSpace(info.Name) == "" {
		return fmt.Errorf("invalid storage file information")
	}
	if err := validateText(info.Name, 4096, true); err != nil {
		return err
	}
	if err := validateText(info.LogicalPath, 64<<10, false); err != nil {
		return err
	}
	return validateText(info.Revision, 4096, true)
}
