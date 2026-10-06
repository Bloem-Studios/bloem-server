package storageplugin

import (
	"context"
	"time"

	"google.golang.org/grpc"
)

// Every RPC is bounded by both the caller and the resident generation lifetime.
// Configure/GetManifest use the same limit; a provider cannot stall startup.
func rpcContext(caller, lifetime context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(caller, limit)
	stop := context.AfterFunc(lifetime, cancel)
	if lifetime.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}
func unaryLifetime(lifetime context.Context, limit time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		bounded, cancel := rpcContext(ctx, lifetime, limit)
		defer cancel()
		return invoke(bounded, method, req, reply, cc, opts...)
	}
}
func streamLifetime(lifetime context.Context, limit time.Duration) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, open grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		bounded, cancel := rpcContext(ctx, lifetime, limit)
		stream, err := open(bounded, desc, cc, method, opts...)
		if err != nil {
			cancel()
			return nil, err
		}
		// Release the lifetime registration even if a consumer abandons a stream.
		context.AfterFunc(bounded, cancel)
		return &boundedStream{ClientStream: stream, cancel: cancel}, nil
	}
}

type boundedStream struct {
	grpc.ClientStream
	cancel context.CancelFunc
}

func (s *boundedStream) RecvMsg(m any) error {
	err := s.ClientStream.RecvMsg(m)
	if err != nil {
		s.cancel()
	}
	return err
}
func (s *boundedStream) SendMsg(m any) error {
	err := s.ClientStream.SendMsg(m)
	if err != nil {
		s.cancel()
	}
	return err
}
