package remotesource

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

// Validators identify one version of the remote media: its size and the
// HTTP validators the server sent with it.
type Validators struct {
	Size         int64
	ETag         string
	LastModified time.Time
}

// Unchanged reports whether fresh describes the same remote media as v, the
// validators of the last probe: the same size, and the same ETag when one was
// recorded, otherwise the same Last-Modified. With neither recorded the
// answer is false, so the media is probed again.
func (v Validators) Unchanged(fresh Validators) bool {
	if v.Size <= 0 || v.Size != fresh.Size {
		return false
	}
	if v.ETag != "" {
		return v.ETag == fresh.ETag
	}
	return !v.LastModified.IsZero() && v.LastModified.Equal(fresh.LastModified)
}

// Validators reports what the server sent with the first block.
func (f *File) Validators() Validators {
	return Validators{Size: f.info.Size, ETag: f.etag, LastModified: f.info.ModifiedAt}
}

// Stat learns the remote size and validators with a one-byte range request
// through client (the guard's), without reading the media. A server that
// ignores ranges answers 200; its Content-Length is the size and the body is
// not read.
func Stat(ctx context.Context, client *http.Client, rawURL string) (Validators, error) {
	if ctx == nil || client == nil {
		return Validators{}, errors.New("remote source: no client")
	}
	if err := ValidateURL(rawURL); err != nil {
		return Validators{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Validators{}, ErrInvalidStrm
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return Validators{}, scrubURL(err)
	}
	defer resp.Body.Close()
	out := Validators{ETag: resp.Header.Get("ETag")}
	if modified, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		out.LastModified = modified.UTC()
	}
	switch resp.StatusCode {
	case http.StatusPartialContent:
		_, _, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok {
			return Validators{}, ErrRangeUnsupported
		}
		out.Size = total
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64))
	case http.StatusOK:
		if resp.ContentLength < 0 {
			return Validators{}, ErrRangeUnsupported
		}
		out.Size = resp.ContentLength
	default:
		return Validators{}, &HTTPStatusError{Status: resp.StatusCode}
	}
	return out, nil
}
