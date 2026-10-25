package storageplugin

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
)

// remaining reports how long the interceptor let one call run.
func remaining(t *testing.T, caller context.Context, limit time.Duration, opts ...grpc.CallOption) time.Duration {
	t.Helper()
	var got time.Duration
	invoke := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("call ran without a deadline")
		}
		got = time.Until(deadline)
		return nil
	}
	if err := unaryLifetime(context.Background(), limit)(caller, "/storage/List", nil, nil, nil, invoke, opts...); err != nil {
		t.Fatal(err)
	}
	return got
}

// A caller that gives one call a longer deadline on purpose, such as a listing
// page, gets it; every other call keeps the session's limit.
func TestUnaryCallsKeepTheSessionLimitUnlessTheyAskForMore(t *testing.T) {
	const limit = 10 * time.Second
	long, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if got := remaining(t, long, limit); got > limit {
		t.Fatalf("plain call ran for %v, want at most the session limit %v", got, limit)
	}
	if got := remaining(t, long, limit, RPCLimit(30*time.Second)); got <= limit || got > 30*time.Second {
		t.Fatalf("call asking for 30s ran for %v", got)
	}
	if got := remaining(t, long, limit, RPCLimit(time.Second)); got <= time.Second {
		t.Fatalf("a shorter request lowered the session limit to %v", got)
	}

	short, cancelShort := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelShort()
	if got := remaining(t, short, limit, RPCLimit(30*time.Second)); got > 2*time.Second {
		t.Fatalf("call outlived its caller's deadline: %v", got)
	}
}
