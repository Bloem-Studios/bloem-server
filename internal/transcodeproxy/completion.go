// Package transcodeproxy owns the private HTTP completion contract shared by
// Silo's integrated API proxy and its dedicated proxy node.
package transcodeproxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
)

const (
	// RequestHeader marks a segment hop whose immediate consumer is another
	// Silo server, not the playback client. The transcode node defers download
	// accounting until that server confirms downstream completion.
	RequestHeader = "X-Silo-Transcode-Proxy"
	// GenerationHeader carries an opaque session-incarnation and FFmpeg-timeline
	// token. It is private to Silo hops and must not be forwarded to the client.
	GenerationHeader = "X-Silo-Transcode-Segment-Generation"
)

var representationRequestHeaders = []string{
	"Range",
	"If-Range",
	"If-Match",
	"If-None-Match",
	"If-Modified-Since",
	"If-Unmodified-Since",
}

// PrepareRequest makes the upstream node serve the same byte representation
// requested by the downstream client and suppresses completion at this hop.
func PrepareRequest(upstream, downstream *http.Request) {
	upstream.Header.Set(RequestHeader, "1")
	for _, name := range representationRequestHeaders {
		for _, value := range downstream.Header.Values(name) {
			upstream.Header.Add(name, value)
		}
	}
}

// CopyResponseHeaders forwards public response metadata while retaining the
// generation token inside the trusted Silo hop.
func CopyResponseHeaders(dst, src http.Header) {
	for name, values := range src {
		if canonical := http.CanonicalHeaderKey(name); canonical == GenerationHeader || canonical == playback.OutputTransferHeaderV3 {
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

// FullRepresentationSize resolves the total entity size needed to distinguish
// a whole-file 206 response from an ordinary partial range.
func FullRepresentationSize(resp *http.Response) int64 {
	if resp == nil {
		return -1
	}
	if resp.StatusCode == http.StatusOK {
		return resp.ContentLength
	}
	if resp.StatusCode != http.StatusPartialContent {
		return -1
	}
	_, total, ok := strings.Cut(resp.Header.Get("Content-Range"), "/")
	if !ok || total == "*" {
		return -1
	}
	size, err := strconv.ParseInt(total, 10, 64)
	if err != nil || size <= 0 {
		return -1
	}
	return size
}

// Acknowledge reports a completed downstream response to the transcode node.
// The opaque generation token makes delayed acknowledgements harmless after a
// restart or reconstruction.
func Acknowledge(ctx context.Context, client *http.Client, targetURL, jwtSecret, generation string) error {
	// Legacy completion may outlive its downstream response cancellation.
	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return acknowledge(ackCtx, client, targetURL, jwtSecret, generation, "", "")
}

// AcknowledgeExecutor retains the final-egress grant context and the exact
// transfer permit. An acknowledgement cannot outlive its bound response authority
// or follow redirects to another node.
func AcknowledgeExecutor(ctx context.Context, client *http.Client, targetURL, jwtSecret, generation, token, permit string) error {
	if token == "" || permit == "" {
		return fmt.Errorf("bound acknowledgement authority required")
	}
	ackCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return acknowledge(ackCtx, client, targetURL, jwtSecret, generation, token, permit)
}

func acknowledge(ackCtx context.Context, client *http.Client, targetURL, jwtSecret, generation, token, permit string) error {
	if client == nil {
		client = http.DefaultClient
	}
	if permit != "" {
		boundedClient := *client
		boundedClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &boundedClient
	}
	req, err := http.NewRequestWithContext(ackCtx, http.MethodPost, targetURL+"/downloaded", nil)
	if err != nil {
		return fmt.Errorf("build acknowledgement: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtSecret)
	req.Header.Set(GenerationHeader, generation)
	if permit != "" {
		req.Header.Set("X-Silo-Stream-Token", token)
		req.Header.Set(playback.OutputTransferHeaderV3, permit)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send acknowledgement: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("acknowledgement status %d", resp.StatusCode)
	}
	return nil
}
