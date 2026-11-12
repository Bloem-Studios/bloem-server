package playback

import (
	"context"
	"errors"
	"testing"
)

type sealingContextMediaHost struct {
	fakeContextMediaHost
	sealed [][]byte
	nodes  []string
	err    error
}

func (h *sealingContextMediaHost) SealWorkerStart(_ context.Context, nodeURL string, start []byte) error {
	h.nodes = append(h.nodes, nodeURL)
	h.sealed = append(h.sealed, start)
	return h.err
}

// S6: a remote transcode start inside a request whose host can seal worker
// starts is sealed first; other requests send it unchanged.
func TestSealContextWorkerStartUsesHostSealer(t *testing.T) {
	if err := SealContextWorkerStart(context.Background(), "http://worker", []byte(`{}`)); err != nil {
		t.Fatalf("no host: %v", err)
	}
	if err := SealContextWorkerStart(WithContextMediaHost(context.Background(), &fakeContextMediaHost{}), "http://worker", []byte(`{}`)); err != nil {
		t.Fatalf("host without sealer: %v", err)
	}
	host := &sealingContextMediaHost{}
	ctx := WithContextMediaHost(context.Background(), host)
	body := []byte(`{"session_id":"s"}`)
	if err := SealContextWorkerStart(ctx, "http://worker", body); err != nil || len(host.sealed) != 1 || string(host.sealed[0]) != string(body) || host.nodes[0] != "http://worker" {
		t.Fatalf("sealer: err=%v sealed=%q nodes=%v", err, host.sealed, host.nodes)
	}
	host.err = errors.New("worker cannot read native storage")
	if err := SealContextWorkerStart(ctx, "http://worker", body); !errors.Is(err, host.err) {
		t.Fatalf("refusal = %v", err)
	}
}
