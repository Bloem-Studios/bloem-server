package api

import (
	"context"
	"errors"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/grpc"

	"github.com/Silo-Server/silo-server/internal/requests"
)

type nativeRoutersFixture struct {
	native  map[int]bool
	err     error
	clients map[int]pluginv1.RequestRouterClient
	calls   []int
}

func (f *nativeRoutersFixture) IsNativeInstallation(_ context.Context, id int) (bool, error) {
	return f.native[id], f.err
}
func (f *nativeRoutersFixture) NativeRequestRouter(_ context.Context, id int, capability string) (pluginv1.RequestRouterClient, error) {
	f.calls = append(f.calls, id)
	if client, ok := f.clients[id]; ok && capability == "ebooks" {
		return client, nil
	}
	return nil, errors.New("no router")
}
func (f *nativeRoutersFixture) NativeRequestRouterDescriptor(_ context.Context, id int, _ string) (*pluginv1.RequestRouterDescriptor, error) {
	return &pluginv1.RequestRouterDescriptor{ApiKeyOptional: true, BaseUrlOptional: true}, nil
}

type namedRouterClient struct {
	pluginv1.RequestRouterClient
	name string
}

func (c namedRouterClient) TestConnection(context.Context, *pluginv1.TestConnectionRequest, ...grpc.CallOption) (*pluginv1.TestConnectionResponse, error) {
	return &pluginv1.TestConnectionResponse{Ok: true, Message: c.name}, nil
}

type fallbackResolverFixture struct{ calls []int }

func (f *fallbackResolverFixture) RequestRouterClient(_ context.Context, id int, _ string) (requests.RouterClient, error) {
	f.calls = append(f.calls, id)
	return nil, errors.New("ordinary plugin runtime")
}
func (f *fallbackResolverFixture) RouterFeatures(context.Context, int, string) (requests.RouterFeatures, error) {
	return requests.RouterFeatures{SupportsSeasons: true}, nil
}

// Request router calls for a native storage installation go to its storage
// session; every other installation stays on the ordinary plugin runtime.
func TestBloemRequestRouterResolverRoutesNativeInstallations(t *testing.T) {
	native := &nativeRoutersFixture{native: map[int]bool{30: true}, clients: map[int]pluginv1.RequestRouterClient{30: namedRouterClient{name: "storage session"}}}
	fallback := &fallbackResolverFixture{}
	resolver := bloemRequestRouterResolver{fallback: fallback, native: native}

	client, err := resolver.RequestRouterClient(t.Context(), 30, "ebooks")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.TestConnection(t.Context(), &pluginv1.TestConnectionRequest{})
	if err != nil || response.GetMessage() != "storage session" {
		t.Fatalf("native installation answered by %v %v", response, err)
	}
	if _, err = resolver.RequestRouterClient(t.Context(), 31, "bookwarehouse"); err == nil || len(fallback.calls) != 1 || fallback.calls[0] != 31 {
		t.Fatalf("ordinary installation not sent to the plugin runtime: %v %v", fallback.calls, err)
	}
	if _, err = resolver.RequestRouterClient(t.Context(), 30, "audiobooks"); err == nil || len(fallback.calls) != 1 {
		t.Fatalf("undeclared native capability fell back to the plugin runtime: %v %v", fallback.calls, err)
	}

	// Features of a native capability come from its own descriptor.
	features, err := resolver.RouterFeatures(t.Context(), 30, "ebooks")
	if err != nil || !features.APIKeyOptional || !features.BaseURLOptional || features.SupportsSeasons {
		t.Fatalf("native features %+v %v", features, err)
	}
	if features, err = resolver.RouterFeatures(t.Context(), 31, "bookwarehouse"); err != nil || !features.SupportsSeasons {
		t.Fatalf("ordinary features %+v %v", features, err)
	}

	// A failed marker lookup fails closed instead of guessing a runtime.
	native.err = errors.New("database unavailable")
	if _, err = resolver.RequestRouterClient(t.Context(), 31, "bookwarehouse"); err == nil || len(fallback.calls) != 1 {
		t.Fatalf("marker failure routed a call: %v %v", fallback.calls, err)
	}

	// Without a native host every call stays on the plugin runtime.
	plain := bloemRequestRouterResolver{fallback: fallback}
	if _, err = plain.RequestRouterClient(t.Context(), 30, "ebooks"); err == nil || len(fallback.calls) != 2 {
		t.Fatalf("no native host: %v %v", fallback.calls, err)
	}
}

// Validate on save goes through the same resolver, so an integration of a
// native installation is validated by the storage plugin.
func TestBloemRequestRouterResolverValidatesThroughNativeSession(t *testing.T) {
	native := &nativeRoutersFixture{native: map[int]bool{30: true}, clients: map[int]pluginv1.RequestRouterClient{30: validatingRouterClient{}}}
	provider := requests.NewPluginRouterProvider(bloemRequestRouterResolver{fallback: &fallbackResolverFixture{}, native: native})
	fields, form, err := provider.Validate(t.Context(), 30, "ebooks", requests.ResolvedRouterConnection{ID: "c"}, nil)
	if err != nil || form != "validated by storage" || len(fields) != 0 {
		t.Fatalf("validate: %v %q %v", fields, form, err)
	}
}

type validatingRouterClient struct{ pluginv1.RequestRouterClient }

func (validatingRouterClient) Validate(context.Context, *pluginv1.ValidateRequest, ...grpc.CallOption) (*pluginv1.ValidateResponse, error) {
	return &pluginv1.ValidateResponse{FormError: "validated by storage"}, nil
}
