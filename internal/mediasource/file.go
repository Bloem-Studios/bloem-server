package mediasource

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

type sourceFile struct {
	ctx      context.Context
	cancel   context.CancelFunc
	source   Source
	ref      Ref
	info     Info
	closed   atomic.Bool
	cursorMu sync.Mutex
	cursor   int64
}

func Open(ctx context.Context, source Source, ref Ref) (File, error) {
	if ctx == nil || source == nil {
		return nil, fmt.Errorf("storage source and context are required")
	}
	if err := validateRef(ref); err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithCancel(ctx)
	info, err := source.Stat(readCtx, ref)
	if err == nil {
		err = validateInfo(info)
	}
	if err == nil && info.Revision != ref.Revision {
		err = fmt.Errorf("storage revision changed")
	}
	if err == nil {
		err = readCtx.Err()
	}
	if err != nil {
		cancel()
		return nil, err
	}
	return &sourceFile{ctx: readCtx, cancel: cancel, source: source, ref: ref, info: info}, nil
}
func (f *sourceFile) Info() Info { return f.info }
func (f *sourceFile) ready() error {
	if f.closed.Load() {
		return os.ErrClosed
	}
	return f.ctx.Err()
}
func (f *sourceFile) Close() error { f.closed.Store(true); f.cancel(); return nil }

type sliceWriter struct {
	remaining []byte
	written   int
}

func (w *sliceWriter) Write(p []byte) (int, error) {
	if len(p) > len(w.remaining) {
		return 0, io.ErrShortWrite
	}
	n := copy(w.remaining, p)
	w.remaining = w.remaining[n:]
	w.written += n
	return n, nil
}
func (f *sourceFile) ReadAt(p []byte, offset int64) (int, error) {
	if err := f.ready(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, fmt.Errorf("negative storage read offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if offset >= f.info.Size {
		return 0, io.EOF
	}
	want := int64(len(p))
	if want > f.info.Size-offset {
		want = f.info.Size - offset
	}
	total := 0
	for int64(total) < want {
		if err := f.ready(); err != nil {
			return total, err
		}
		length := want - int64(total)
		if length > maxRange {
			length = maxRange
		}
		writer := &sliceWriter{remaining: p[total : total+int(length)]}
		err := f.source.ReadRange(f.ctx, f.ref, offset+int64(total), length, writer)
		total += writer.written
		if err != nil {
			return total, err
		}
		if writer.written != int(length) {
			return total, io.ErrUnexpectedEOF
		}
	}
	if total < len(p) {
		return total, io.EOF
	}
	return total, nil
}
func (f *sourceFile) Read(p []byte) (int, error) {
	f.cursorMu.Lock()
	defer f.cursorMu.Unlock()
	n, err := f.ReadAt(p, f.cursor)
	f.cursor += int64(n)
	return n, err
}
func (f *sourceFile) Seek(offset int64, whence int) (int64, error) {
	f.cursorMu.Lock()
	defer f.cursorMu.Unlock()
	if err := f.ready(); err != nil {
		return 0, err
	}
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = f.cursor
	case io.SeekEnd:
		base = f.info.Size
	default:
		return 0, fmt.Errorf("invalid seek origin")
	}
	if (offset > 0 && base > 1<<63-1-offset) || (offset < 0 && offset < -base) {
		return 0, fmt.Errorf("invalid seek offset")
	}
	f.cursor = base + offset
	return f.cursor, nil
}
