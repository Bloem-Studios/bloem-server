package mediasource

import (
	"context"
	"fmt"
	"os"
)

type localFile struct {
	file *os.File
	ctx  context.Context
	info Info
	stop func() bool
}

// OpenLocal accepts only a path the caller has already authorized.
func OpenLocal(ctx context.Context, authorizedPath string) (File, error) {
	if ctx == nil {
		return nil, fmt.Errorf("storage context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(authorizedPath)
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("media is not a regular file")
	}
	reader := &localFile{file: f, ctx: ctx, info: Info{Name: stat.Name(), LogicalPath: authorizedPath, Size: stat.Size(), ModifiedAt: stat.ModTime()}}
	reader.stop = context.AfterFunc(ctx, func() { _ = f.Close() })
	return reader, nil
}
func (f *localFile) Info() Info   { return f.info }
func (f *localFile) Close() error { f.stop(); return f.file.Close() }
func (f *localFile) Read(p []byte) (int, error) {
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	return f.file.Read(p)
}
func (f *localFile) ReadAt(p []byte, offset int64) (int, error) {
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	return f.file.ReadAt(p, offset)
}
func (f *localFile) Seek(offset int64, whence int) (int64, error) {
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	return f.file.Seek(offset, whence)
}
