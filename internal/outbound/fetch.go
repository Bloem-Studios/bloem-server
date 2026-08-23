package outbound

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Request describes a bounded GET through the guarded outbound transport.
type Request struct {
	URL      string
	MaxBytes int64
	Statuses map[int]struct{}
}

// Response is the bounded result of an outbound fetch.
type Response struct {
	FinalURL    *url.URL
	ContentType string
	Body        []byte
}

// Fetch performs one bounded GET. By default, any 2xx response is accepted.
func (c *Client) Fetch(ctx context.Context, request Request) (*Response, error) {
	if request.MaxBytes <= 0 {
		return nil, fmt.Errorf("outbound: positive response limit is required")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, request.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("outbound: build request: %w", err)
	}
	response, err := c.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if !statusAllowed(response.StatusCode, request.Statuses) {
		return nil, fmt.Errorf("outbound: unexpected status %d", response.StatusCode)
	}
	if response.ContentLength > request.MaxBytes {
		return nil, fmt.Errorf("outbound: response exceeds %d byte limit", request.MaxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, request.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("outbound: read response: %w", err)
	}
	if int64(len(body)) > request.MaxBytes {
		return nil, fmt.Errorf("outbound: response exceeds %d byte limit", request.MaxBytes)
	}
	return &Response{
		FinalURL:    response.Request.URL,
		ContentType: response.Header.Get("Content-Type"),
		Body:        body,
	}, nil
}

func statusAllowed(status int, allowed map[int]struct{}) bool {
	if len(allowed) == 0 {
		return status >= 200 && status < 300
	}
	_, ok := allowed[status]
	return ok
}
