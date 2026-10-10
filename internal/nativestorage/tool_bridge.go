package nativestorage

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func newToolCapability() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func (t *ToolInput) serveToolRange(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	if t.closing || t.ctx.Err() != nil {
		t.mu.Unlock()
		http.Error(w, "tool input unavailable", http.StatusServiceUnavailable)
		return
	}
	t.handlers.Add(1)
	t.mu.Unlock()
	defer t.handlers.Done()
	w.Header().Set("Cache-Control", "no-store")
	if r.Host != t.host || r.URL.EscapedPath() != "/"+t.capability || r.URL.RawQuery != "" || r.URL.ForceQuery || (r.URL.Host != "" && r.URL.Host != t.host) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	values := r.Header.Values("Range")
	if len(values) > 1 {
		toolRangeNotSatisfiable(w, t.info.Size)
		return
	}
	byteRange := r.Header.Get("Range")
	start, length, err := toolByteRange(byteRange, t.info.Size)
	if err != nil {
		toolRangeNotSatisfiable(w, t.info.Size)
		return
	}
	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	default:
		t.fail(ErrToolInputBusy)
		http.Error(w, "tool input unavailable", http.StatusTooManyRequests)
		return
	}
	if err := t.config.Reauthorize(t.ctx); err != nil {
		t.fail(err)
		http.Error(w, "tool input unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := t.ctx.Err(); err != nil {
		t.fail(err)
		http.Error(w, "tool input unavailable", http.StatusServiceUnavailable)
		return
	}
	status := http.StatusOK
	if byteRange != "" {
		status = http.StatusPartialContent
	}
	writeHeaders := func() {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		if status == http.StatusPartialContent {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, t.info.Size))
		}
		w.WriteHeader(status)
	}
	if r.Method == http.MethodHead || length == 0 {
		writeHeaders()
		return
	}
	buffer := make([]byte, int(min(length, int64(t.config.ChunkBytes))))
	started := false
	for remaining := length; remaining > 0; {
		// A tool may deliberately close a successful HTTP stream in order to
		// seek. That is not a provider error; no next read is admitted for it.
		if r.Context().Err() != nil {
			return
		}
		chunk := buffer[:int(min(remaining, int64(len(buffer))))]
		if err := t.readToolChunk(chunk, start); err != nil {
			t.fail(err)
			if started {
				panic(http.ErrAbortHandler) // Truncate the fixed-length stream.
			}
			http.Error(w, "tool input unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.Context().Err() != nil {
			return
		}
		if !started {
			writeHeaders()
			started = true
		}
		if _, err := w.Write(chunk); err != nil {
			return
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		start += int64(len(chunk))
		remaining -= int64(len(chunk))
	}
}

// readToolChunk buffers a complete bounded read before HTTP sees any byte.
// Even n==len(buffer) accompanied by an error must invalidate all tool output.
func (t *ToolInput) readToolChunk(buffer []byte, offset int64) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	if err := t.config.Reauthorize(t.ctx); err != nil {
		return err
	}
	t.mu.Lock()
	if int64(len(buffer)) > t.config.MaxBytes-t.spent {
		t.mu.Unlock()
		return ErrToolInputBudget
	}
	t.spent += int64(len(buffer))
	t.mu.Unlock()
	n, err := t.file.ReadAt(buffer, offset)
	if err != nil {
		clear(buffer)
		return err
	}
	if n != len(buffer) {
		clear(buffer)
		return io.ErrUnexpectedEOF
	}
	if err := t.ctx.Err(); err != nil {
		clear(buffer)
		return err
	}
	if err := t.config.Reauthorize(t.ctx); err != nil {
		clear(buffer)
		return err
	}
	if err := t.ctx.Err(); err != nil {
		clear(buffer)
		return err
	}
	return nil
}

func toolRangeNotSatisfiable(w http.ResponseWriter, size int64) {
	w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
	http.Error(w, "unsupported byte range", http.StatusRequestedRangeNotSatisfiable)
}

// Only a single byte range is supported. Parsing bounds precede arithmetic,
// including suffix/open-ended forms, so untrusted headers cannot overflow.
func toolByteRange(value string, size int64) (int64, int64, error) {
	if value == "" {
		return 0, size, nil
	}
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") || size == 0 {
		return 0, 0, errors.New("unsupported range")
	}
	first, last, ok := strings.Cut(strings.TrimPrefix(value, "bytes="), "-")
	if !ok {
		return 0, 0, errors.New("unsupported range")
	}
	number := func(value string) (int64, error) {
		if value == "" {
			return 0, errors.New("invalid range")
		}
		for _, char := range value {
			if char < '0' || char > '9' {
				return 0, errors.New("invalid range")
			}
		}
		return strconv.ParseInt(value, 10, 64)
	}
	if first == "" {
		suffix, err := number(last)
		if err != nil || suffix <= 0 {
			return 0, 0, errors.New("unsupported range")
		}
		length := min(suffix, size)
		return size - length, length, nil
	}
	start, err := number(first)
	if err != nil || start >= size {
		return 0, 0, errors.New("unsupported range")
	}
	end := size - 1
	if last != "" {
		end, err = number(last)
		if err != nil || end < start {
			return 0, 0, errors.New("unsupported range")
		}
		end = min(end, size-1)
	}
	return start, end - start + 1, nil
}
